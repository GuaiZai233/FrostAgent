package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
)

const MaxFileBytes int64 = 30 << 20
const fileChunkBytes = 384 << 10

// FilePath recognizes sandbox references without ever falling back to host files.
// Ordinary paths outside /sandbox remain owned by the delivery adapter.
func FilePath(raw string) (string, bool, error) {
	name := raw
	explicit := strings.HasPrefix(raw, "sandbox:")
	if explicit {
		u, err := url.Parse(raw)
		if err != nil || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", true, errors.New("无效的 sandbox 文件路径")
		}
		name = u.Path
	}
	if !explicit && name != "/sandbox" && !strings.HasPrefix(name, "/sandbox/") {
		return "", false, nil
	}
	if len(name) > 4096 || strings.ContainsAny(name, "\\\x00") || !strings.HasPrefix(name, "/sandbox/") {
		return "", true, errors.New("沙箱产物必须位于 /sandbox/ 内")
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." {
			return "", true, errors.New("沙箱产物路径不能包含 ..")
		}
	}
	name = path.Clean(name)
	if name == "/sandbox" {
		return "", true, errors.New("沙箱产物必须是文件")
	}
	return name, true, nil
}

type fileChunkRequest struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Limit  int64  `json:"limit"`
}

type fileChunkResponse struct {
	Size        int64  `json:"size"`
	Fingerprint string `json:"fingerprint"`
	Data        string `json:"data"`
	Error       string `json:"error"`
}

// Each request opens a regular file beneath a directory descriptor. Symlinks,
// devices and pipes cannot redirect the read outside the artifact directory.
// Payloads stay below the gateway's stdout limit and never enter tool results.
const fileReadPython = `import os,sys,json,base64,stat
q=json.loads(base64.b64decode(sys.argv[1]))
fds=[]
try:
    fd=os.open(q["root"],os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    fds.append(fd)
    parts=q["path"].split("/")
    if not parts or any(p in ("", ".", "..") for p in parts):
        raise ValueError("invalid_path")
    for part in parts[:-1]:
        fd=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
        fds.append(fd)
    fd=os.open(parts[-1],os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
    fds.append(fd)
    before=os.fstat(fd)
    if not stat.S_ISREG(before.st_mode):
        raise ValueError("not_regular")
    if before.st_size<=0 or before.st_size>q["limit"]:
        raise ValueError("size_limit")
    def fingerprint(s):
        return ":".join(str(v) for v in (s.st_dev,s.st_ino,s.st_size,s.st_mtime_ns,s.st_ctime_ns))
    os.lseek(fd,q["offset"],os.SEEK_SET)
    data=os.read(fd,384*1024)
    after=os.fstat(fd)
    if fingerprint(before)!=fingerprint(after):
        raise ValueError("file_changed")
    print(json.dumps({"size":before.st_size,"fingerprint":fingerprint(before),"data":base64.b64encode(data).decode("ascii")}))
except ValueError as e:
    print(json.dumps({"error":str(e)}))
except OSError:
    print(json.dumps({"error":"unreadable"}))
finally:
    for fd in reversed(fds):
        os.close(fd)
`

func fileReadCommand(req fileChunkRequest) string {
	data, _ := json.Marshal(req)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	return "python3 -I -c " + quote(fileReadPython) + " " + quote(base64.StdEncoding.EncodeToString(data))
}

// ReadFile transfers one current-session artifact through the existing sandbox
// protocol. The backend remains authoritative for instance/session isolation.
func ReadFile(ctx context.Context, backend Backend, sessionID, name string, limit int64) ([]byte, error) {
	if backend == nil {
		return nil, ErrSandboxDisabled
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("读取沙箱产物需要当前来源会话")
	}
	name, recognized, err := FilePath(name)
	if err != nil {
		return nil, err
	}
	if !recognized || limit <= 0 || limit > MaxFileBytes {
		return nil, errors.New("无效的沙箱文件路径或传输大小限制")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxExecutionTimeout)
	defer cancel()
	var data []byte
	var fingerprint string
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := backend.Exec(ctx, ExecRequest{
			SessionID: sessionID, Cwd: "/sandbox", Timeout: 30 * time.Second,
			Command: fileReadCommand(fileChunkRequest{Root: "/sandbox", Path: strings.TrimPrefix(name, "/sandbox/"), Offset: int64(len(data)), Limit: limit}),
		})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if result.TimedOut || result.ExitCode == nil || *result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated || len(result.Stdout) > 2*fileChunkBytes {
			return nil, errors.New("沙箱文件传输失败或输出被截断")
		}
		var chunk fileChunkResponse
		if err := json.Unmarshal([]byte(result.Stdout), &chunk); err != nil {
			return nil, errors.New("沙箱文件返回了无效的传输数据")
		}
		if chunk.Error != "" {
			switch chunk.Error {
			case "size_limit":
				return nil, errors.New("沙箱文件为空或超出剩余的 30 MiB 传输额度")
			case "file_changed":
				return nil, errors.New("沙箱文件在传输期间发生变化，请完成生成后再发送")
			default:
				return nil, errors.New("沙箱文件不存在、不可读或不是普通文件；不支持符号链接")
			}
		}
		if chunk.Size <= 0 || chunk.Size > limit || chunk.Fingerprint == "" || len(chunk.Fingerprint) > 256 {
			return nil, errors.New("沙箱文件大小或标识无效")
		}
		if fingerprint == "" {
			size, fingerprint = chunk.Size, chunk.Fingerprint
			data = make([]byte, 0, int(size))
		} else if chunk.Size != size || chunk.Fingerprint != fingerprint {
			return nil, errors.New("沙箱文件在传输期间发生变化，请完成生成后再发送")
		}
		want := min(int64(fileChunkBytes), size-int64(len(data)))
		if len(chunk.Data) != base64.StdEncoding.EncodedLen(int(want)) {
			return nil, errors.New("沙箱文件分块不完整")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(chunk.Data)
		if err != nil || int64(len(decoded)) != want {
			return nil, errors.New("沙箱文件分块编码无效")
		}
		data = append(data, decoded...)
		if int64(len(data)) == size {
			return data, nil
		}
	}
}
