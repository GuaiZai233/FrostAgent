package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func decodeWriteCommand(t *testing.T, command string) fileWriteRequest {
	t.Helper()
	i := strings.LastIndex(command, " '")
	if i < 0 || !strings.HasSuffix(command, "'") {
		t.Fatal("missing transfer request")
	}
	data, err := base64.StdEncoding.DecodeString(command[i+2 : len(command)-1])
	if err != nil {
		t.Fatal(err)
	}
	var q fileWriteRequest
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}
	return q
}

func writeResult(output string) ExecResult {
	code := 0
	return ExecResult{Stdout: output, ExitCode: &code}
}

func TestWriteFileChunksAndAtomicCommit(t *testing.T) {
	data := bytes.Repeat([]byte("document fixture"), 7000)
	var transferred []byte
	committed := false
	stage := ""
	backend := &stubBackend{execFunc: func(_ context.Context, req ExecRequest) (ExecResult, error) {
		q := decodeWriteCommand(t, req.Command)
		if req.SessionID != "source-session" || req.Cwd != "/sandbox" || len(req.Command) > MaxCommandLength || q.Path != "received/fixture.docx" || q.Size != int64(len(data)) {
			t.Fatalf("incorrect file write request: session=%s path=%s", req.SessionID, q.Path)
		}
		if stage == "" {
			stage = q.Stage
		} else if q.Stage != stage {
			t.Fatal("upload staging path changed")
		}
		switch q.Op {
		case "write":
			part, err := base64.StdEncoding.DecodeString(q.Data)
			if err != nil || len(part) > fileWriteChunkBytes || q.Offset != len(transferred) {
				t.Fatal("invalid chunk")
			}
			transferred = append(transferred, part...)
		case "commit":
			hash := sha256.Sum256(transferred)
			if !bytes.Equal(transferred, data) || q.Hash != hex.EncodeToString(hash[:]) || q.Data != "" {
				t.Fatal("incomplete commit")
			}
			committed = true
		default:
			t.Fatalf("unexpected operation %s", q.Op)
		}
		return writeResult(`{"ok":true}`), nil
	}}
	if err := WriteFile(context.Background(), backend, "source-session", "/sandbox/received/fixture.docx", data); err != nil || !committed {
		t.Fatalf("write: %v committed=%v", err, committed)
	}
}

func TestWriteFileFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"transport", "exit", "timeout", "truncated", "malformed", "remote", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writes := 0
			backend := &stubBackend{execFunc: func(_ context.Context, req ExecRequest) (ExecResult, error) {
				q := decodeWriteCommand(t, req.Command)
				if q.Op == "commit" {
					t.Fatal("failed upload published")
				}
				if q.Op == "abort" {
					return writeResult(`{"ok":true}`), nil
				}
				writes++
				r := writeResult(`{"ok":true}`)
				switch mode {
				case "transport":
					return ExecResult{}, errors.New("transport unavailable")
				case "exit":
					code := 1
					r.ExitCode = &code
				case "timeout":
					r.TimedOut = true
				case "truncated":
					r.StdoutTruncated = true
				case "malformed":
					r.Stdout = "invalid"
				case "remote":
					r.Stdout = `{"error":"unavailable"}`
				case "cancel":
					cancel()
				}
				return r, nil
			}}
			if err := WriteFile(ctx, backend, "s", "/sandbox/a", bytes.Repeat([]byte{1}, fileWriteChunkBytes+1)); err == nil || writes != 1 {
				t.Fatalf("failure accepted: %v writes=%d", err, writes)
			}
		})
	}
	backend := &stubBackend{execFunc: func(context.Context, ExecRequest) (ExecResult, error) {
		t.Fatal("invalid request reached sandbox")
		return ExecResult{}, nil
	}}
	for _, q := range []struct {
		session, path string
		data          []byte
	}{{"", "/sandbox/a", []byte{1}}, {"s", "/tmp/a", []byte{1}}, {"s", "/sandbox/../a", []byte{1}}, {"s", "/sandbox/a", nil}, {"s", "/sandbox/a", make([]byte, MaxFileBytes+1)}} {
		if err := WriteFile(context.Background(), backend, q.session, q.path, q.data); err == nil {
			t.Fatal("invalid write accepted")
		}
	}
	if err := WriteFile(context.Background(), nil, "s", "/sandbox/a", []byte{1}); !errors.Is(err, ErrSandboxDisabled) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestFileWriterPythonProtocol(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sandbox file writer uses Linux directory descriptors")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	inside := filepath.Join(root, "inbox")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"json", "base64", "hashlib", "stat"} {
		if err := os.WriteFile(filepath.Join(inside, module+".py"), []byte("raise RuntimeError('unsafe import')"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	backend := &stubBackend{execFunc: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
		q := decodeWriteCommand(t, req.Command)
		q.Root = inside
		command := exec.CommandContext(ctx, "sh", "-c", fileWriteCommand(q))
		command.Dir = inside
		command.Env = append(os.Environ(), "PYTHONPATH="+inside)
		output, err := command.Output()
		if err != nil {
			if failed, ok := err.(*exec.ExitError); ok {
				return ExecResult{}, fmt.Errorf("writer Python failed: %s", failed.Stderr)
			}
			return ExecResult{}, err
		}
		return writeResult(string(output)), nil
	}}
	data := bytes.Repeat([]byte("DOCX fixture bytes"), 3000)
	name := "received/复习题 '$(touch forbidden).docx"
	if err := WriteFile(context.Background(), backend, "s", "/sandbox/"+name, data); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(inside, name))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("writer did not preserve attachment bytes")
	}
	hash := sha256.Sum256(data)
	present, err := FilePresent(context.Background(), backend, "s", "/sandbox/"+name, int64(len(data)), hex.EncodeToString(hash[:]))
	if err != nil || !present {
		t.Fatalf("file presence: %v %v", present, err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(inside, "parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(inside, "link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"parent/outside", "link"} {
		if err := WriteFile(context.Background(), backend, "s", "/sandbox/"+path, data); err == nil {
			t.Fatalf("symlink write accepted: %s", path)
		}
	}
	protected, _ := os.ReadFile(filepath.Join(root, "outside"))
	if string(protected) != "protected" {
		t.Fatal("escaped sandbox root")
	}
	if _, err := os.Stat(filepath.Join(inside, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("filename executed as shell code")
	}
	if err := os.WriteFile(filepath.Join(inside, name), []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	present, err = FilePresent(context.Background(), backend, "s", "/sandbox/"+name, int64(len(data)), hex.EncodeToString(hash[:]))
	if err != nil || present {
		t.Fatalf("changed file accepted: %v %v", present, err)
	}
}
