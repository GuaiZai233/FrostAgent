package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"FrostAgent/internal/sandbox"
)

var sandboxDirectory = regexp.MustCompile("^/sandbox/frostagent-skill-[a-zA-Z0-9]+$")

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Run copies validated files into a fresh session sandbox directory before running
// a command. No bundled code, shell interpolation or host command runs on import.
func Run(ctx context.Context, backend sandbox.Backend, sessionID string, pkg Package, command string, timeout time.Duration) (sandbox.ExecResult, string, error) {
	if backend == nil {
		return sandbox.ExecResult{}, "", sandbox.ErrSandboxDisabled
	}
	if strings.TrimSpace(sessionID) == "" {
		return sandbox.ExecResult{}, "", errors.New("技能执行需要当前会话")
	}
	if strings.TrimSpace(command) == "" || len(command) > sandbox.MaxCommandLength {
		return sandbox.ExecResult{}, "", errors.New("command 不能为空或超出限制")
	}
	if timeout <= 0 || timeout > sandbox.MaxExecutionTimeout {
		return sandbox.ExecResult{}, "", errors.New("timeout 必须在 1–120 秒内")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	exec := func(command, cwd string) (sandbox.ExecResult, error) {
		return backend.Exec(ctx, sandbox.ExecRequest{SessionID: sessionID, Command: command, Cwd: cwd, Timeout: timeout})
	}
	preparation := func(command, cwd string) (sandbox.ExecResult, error) {
		result, err := exec(command, cwd)
		if err != nil {
			return result, err
		}
		if result.TimedOut || result.ExitCode == nil || *result.ExitCode != 0 {
			return result, fmt.Errorf("技能资源准备失败: %s", result.Stderr)
		}
		return result, nil
	}
	created, err := preparation("mktemp -d /sandbox/frostagent-skill-XXXXXXXXXXXX", "/sandbox")
	if err != nil {
		return sandbox.ExecResult{}, "", err
	}
	cwd := strings.TrimSpace(created.Stdout)
	if !sandboxDirectory.MatchString(cwd) {
		return sandbox.ExecResult{}, "", errors.New("沙箱返回了无效技能工作目录")
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !ValidPath(name) {
			return sandbox.ExecResult{}, "", errors.New("技能资源路径无效")
		}
		member, err := writer.Create(name)
		if err != nil {
			return sandbox.ExecResult{}, "", err
		}
		if _, err := member.Write(pkg.Files[name]); err != nil {
			return sandbox.ExecResult{}, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return sandbox.ExecResult{}, "", err
	}
	transfer := ".frostagent-transfer"
	for index := 0; ; index++ {
		transfer = fmt.Sprintf(".frostagent-transfer-%d", index)
		exists := false
		for name := range pkg.Files {
			if name == transfer || strings.HasPrefix(name, transfer+"/") {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
	}
	payload := base64.StdEncoding.EncodeToString(buffer.Bytes())
	for offset := 0; offset < len(payload); offset += 48000 {
		end := min(offset+48000, len(payload))
		command := "printf '%s' " + shellQuote(payload[offset:end]) + " >> " + shellQuote(transfer)
		if _, err := preparation(command, cwd); err != nil {
			return sandbox.ExecResult{}, cwd, err
		}
	}
	unpack := "python3 -c " + shellQuote("import base64,io,pathlib,zipfile; p=pathlib.Path("+shellQuote(transfer)+"); z=zipfile.ZipFile(io.BytesIO(base64.b64decode(p.read_bytes()))); z.extractall('.'); p.unlink()")
	if _, err := preparation(unpack, cwd); err != nil {
		return sandbox.ExecResult{}, cwd, err
	}
	command = "export CLAUDE_SKILL_DIR=" + shellQuote(cwd) + "; export SKILL_DIR=" + shellQuote(cwd) + "; " + command
	result, err := exec(command, cwd)
	return result, cwd, err
}
