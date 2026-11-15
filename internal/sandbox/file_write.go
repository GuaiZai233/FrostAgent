package sandbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	fileWriteChunkBytes     = 32 << 10
	fileWriteConcurrency    = 8
	fileWriteCleanupTimeout = 5 * time.Second
)

type fileWriteRequest struct {
	Root   string `json:"root"`
	Path   string `json:"path"`
	Stage  string `json:"stage,omitempty"`
	Op     string `json:"op"`
	Size   int64  `json:"size"`
	Hash   string `json:"hash"`
	Offset int    `json:"offset"`
	Data   string `json:"data,omitempty"`
}

// Directory descriptors and atomic publication keep partial uploads and symlink
// traversal out of the model-visible attachment path. No uploaded code executes.
const fileWritePython = `import os,sys,json,base64,stat,hashlib
q=json.loads(base64.b64decode(sys.argv[1]))
fds=[]
try:
    fd=os.open(q["root"],os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    fds.append(fd)
    parts=q["path"].split("/")
    if not parts or any(p in ("", ".", "..") for p in parts):
        raise ValueError("invalid_path")
    for part in parts[:-1]:
        if q["op"] not in ("check", "abort"):
            try: os.mkdir(part,0o700,dir_fd=fd)
            except FileExistsError: pass
        fd=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
        fds.append(fd)
    def matches(name):
        f=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
        try:
            s=os.fstat(f)
            if not stat.S_ISREG(s.st_mode) or s.st_size!=q["size"]: return False
            h=hashlib.sha256()
            while True:
                b=os.read(f,384*1024)
                if not b: break
                h.update(b)
            return h.hexdigest()==q["hash"]
        finally: os.close(f)
    if q["op"]=="check":
        print(json.dumps({"present":matches(parts[-1])}))
    else:
        stage=q["stage"]
        if not stage.startswith(".frostagent-upload-") or "/" in stage or "\\" in stage:
            raise ValueError("invalid_stage")
        if q["op"] in ("begin", "write"):
            flags=os.O_WRONLY|os.O_NOFOLLOW|os.O_NONBLOCK
            if q["op"]=="begin": flags|=os.O_CREAT|os.O_EXCL
            f=os.open(stage,flags,0o600,dir_fd=fd)
            try:
                s=os.fstat(f)
                b=base64.b64decode(q["data"],validate=True)
                expected_size=0 if q["op"]=="begin" else q["size"]
                if not stat.S_ISREG(s.st_mode) or s.st_size!=expected_size or q["offset"]<0 or q["offset"]+len(b)>q["size"]:
                    raise ValueError("invalid_chunk")
                if q["op"]=="begin":
                    if q["offset"]!=0: raise ValueError("invalid_chunk")
                    os.ftruncate(f,q["size"])
                offset=q["offset"]
                view=memoryview(b)
                while view:
                    n=os.pwrite(f,view,offset)
                    if n<=0: raise ValueError("invalid_write")
                    offset+=n
                    view=view[n:]
            finally: os.close(f)
        elif q["op"]=="commit":
            if not matches(stage): raise ValueError("invalid_content")
            try:
                s=os.stat(parts[-1],dir_fd=fd,follow_symlinks=False)
                if not stat.S_ISREG(s.st_mode): raise ValueError("invalid_target")
            except FileNotFoundError: pass
            os.replace(stage,parts[-1],src_dir_fd=fd,dst_dir_fd=fd)
        elif q["op"]=="abort":
            try: os.unlink(stage,dir_fd=fd)
            except FileNotFoundError: pass
        else: raise ValueError("invalid_op")
        print(json.dumps({"ok":True}))
except (OSError,ValueError):
    print(json.dumps({"error":"unavailable"}))
finally:
    for f in reversed(fds): os.close(f)
`

func fileWriteCommand(req fileWriteRequest) string {
	data, _ := json.Marshal(req)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	return "python3 -I -c " + quote(fileWritePython) + " " + quote(base64.StdEncoding.EncodeToString(data))
}

func fileWriteExec(ctx context.Context, backend Backend, sessionID string, req fileWriteRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	command := fileWriteCommand(req)
	if len(command) > MaxCommandLength {
		return false, errors.New("沙箱文件传输命令超出限制")
	}
	result, err := backend.Exec(ctx, ExecRequest{SessionID: sessionID, Command: command, Cwd: "/sandbox", Timeout: 30 * time.Second})
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var response struct {
		OK      bool   `json:"ok"`
		Present bool   `json:"present"`
		Error   string `json:"error"`
	}
	if result.TimedOut || result.ExitCode == nil || *result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated || len(result.Stdout) > 1024 || json.Unmarshal([]byte(result.Stdout), &response) != nil {
		return false, errors.New("沙箱文件导入失败或传输响应无效")
	}
	if req.Op == "check" {
		return response.Error == "" && response.Present, nil
	}
	if response.Error != "" || !response.OK {
		return false, errors.New("沙箱文件导入失败，目录或文件不可安全写入")
	}
	return true, nil
}

func writeFileRequest(sessionID, name string, size int64, hash string) (fileWriteRequest, error) {
	name, recognized, err := FilePath(name)
	if err != nil || !recognized || len(name) > 1024 || strings.TrimSpace(sessionID) == "" || size <= 0 || size > MaxFileBytes || len(hash) != 64 {
		return fileWriteRequest{}, errors.New("无效的会话附件路径、大小或校验值")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return fileWriteRequest{}, errors.New("无效的附件校验值")
	}
	return fileWriteRequest{Root: "/sandbox", Path: strings.TrimPrefix(name, "/sandbox/"), Size: size, Hash: hash}, nil
}

// FilePresent revalidates a cached attachment after sandbox expiry or model edits.
func FilePresent(ctx context.Context, backend Backend, sessionID, name string, size int64, hash string) (bool, error) {
	if backend == nil {
		return false, ErrSandboxDisabled
	}
	req, err := writeFileRequest(sessionID, name, size, hash)
	if err != nil {
		return false, err
	}
	req.Op = "check"
	return fileWriteExec(ctx, backend, sessionID, req)
}

// WriteFile imports bytes into the current session, publishing only a complete,
// hash-verified regular file. It never reads or writes the FrostAgent host.
func WriteFile(ctx context.Context, backend Backend, sessionID, name string, data []byte) error {
	if backend == nil {
		return ErrSandboxDisabled
	}
	hash := sha256.Sum256(data)
	req, err := writeFileRequest(sessionID, name, int64(len(data)), hex.EncodeToString(hash[:]))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, MaxExecutionTimeout)
	defer cancel()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return errors.New("无法创建沙箱附件传输标识")
	}
	req.Stage = ".frostagent-upload-" + hex.EncodeToString(nonce[:])
	completed := false
	started := false
	defer func() {
		if started && !completed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), fileWriteCleanupTimeout)
			defer cleanupCancel()
			cleanup := req
			cleanup.Op, cleanup.Data = "abort", ""
			_, _ = fileWriteExec(cleanupCtx, backend, sessionID, cleanup)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	started = true
	first := req
	first.Op, first.Data = "begin", base64.StdEncoding.EncodeToString(data[:min(fileWriteChunkBytes, len(data))])
	if _, err := fileWriteExec(ctx, backend, sessionID, first); err != nil {
		return err
	}
	if err := writeFileChunks(ctx, backend, sessionID, req, data); err != nil {
		return err
	}
	req.Op, req.Data = "commit", ""
	_, err = fileWriteExec(ctx, backend, sessionID, req)
	completed = err == nil
	return err
}

// Each worker writes disjoint byte ranges in the preallocated staging file.
// Join every worker before committing or unlinking it, including on cancellation.
func writeFileChunks(ctx context.Context, backend Backend, sessionID string, req fileWriteRequest, data []byte) error {
	parentCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	var next, failed sync.Mutex
	offset := fileWriteChunkBytes
	var writeErr error
	for range min(fileWriteConcurrency, (len(data)-1)/fileWriteChunkBytes) {
		workers.Go(func() {
			for ctx.Err() == nil {
				next.Lock()
				start := offset
				offset += fileWriteChunkBytes
				next.Unlock()
				if start >= len(data) {
					return
				}
				chunk := req
				chunk.Op, chunk.Offset, chunk.Data = "write", start, base64.StdEncoding.EncodeToString(data[start:min(start+fileWriteChunkBytes, len(data))])
				if _, err := fileWriteExec(ctx, backend, sessionID, chunk); err != nil {
					failed.Lock()
					if writeErr == nil {
						writeErr = err
						cancel()
					}
					failed.Unlock()
					return
				}
			}
		})
	}
	workers.Wait()
	if err := parentCtx.Err(); err != nil {
		return err
	}
	if writeErr != nil {
		return writeErr
	}
	return ctx.Err()
}
