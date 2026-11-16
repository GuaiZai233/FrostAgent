package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFilePath(t *testing.T) {
	for _, raw := range []string{"/sandbox/chart.png", "sandbox:/sandbox/chart.png", "sandbox:///sandbox/chart.png"} {
		name, ok, err := FilePath(raw)
		if err != nil || !ok || name != "/sandbox/chart.png" {
			t.Fatalf("%q: %q %v %v", raw, name, ok, err)
		}
	}
	for _, raw := range []string{"", "/tmp/host.png", "https://example.invalid/image", "/sandbox-other/a"} {
		if _, ok, err := FilePath(raw); ok || err != nil {
			t.Fatalf("host/URL path misclassified: %q %v %v", raw, ok, err)
		}
	}
	for _, raw := range []string{"/sandbox", "/sandbox/../host", "/sandbox/a/../../host", "/sandbox/a\x00b", `sandbox:/sandbox/a\b`, "sandbox:/tmp/a", "sandbox://other/sandbox/a", "sandbox:relative", "sandbox:/sandbox/a?token=secret", "sandbox:/sandbox/%2e%2e/host"} {
		if _, ok, err := FilePath(raw); !ok || err == nil {
			t.Fatalf("invalid sandbox path accepted: %q %v %v", raw, ok, err)
		}
	}
}

func decodeFileCommand(t *testing.T, command string) fileChunkRequest {
	t.Helper()
	start := strings.LastIndex(command, " '")
	if start < 0 || !strings.HasSuffix(command, "'") {
		t.Fatal("missing encoded file request")
	}
	data, err := base64.StdEncoding.DecodeString(command[start+2 : len(command)-1])
	if err != nil {
		t.Fatal(err)
	}
	var req fileChunkRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func fileResult(t *testing.T, response fileChunkResponse) ExecResult {
	t.Helper()
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	code := 0
	return ExecResult{Stdout: string(encoded), ExitCode: &code}
}

func TestReadFileUsesSourceSessionAndBoundedChunks(t *testing.T) {
	want := bytes.Repeat([]byte("artifact-data"), fileChunkBytes/4)
	calls := 0
	backend := &stubBackend{execFunc: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
		q := decodeFileCommand(t, req.Command)
		if req.SessionID != "source-session" || req.Cwd != "/sandbox" || req.Timeout <= 0 || req.Timeout > MaxExecutionTimeout || q.Root != "/sandbox" || q.Path != "report.pdf" || q.Offset != int64(calls*fileChunkBytes) || q.Limit != int64(len(want)) {
			t.Fatalf("incorrect request: %+v %+v", req, q)
		}
		calls++
		end := min(int(q.Offset)+fileChunkBytes, len(want))
		return fileResult(t, fileChunkResponse{Size: int64(len(want)), Fingerprint: "stable", Data: base64.StdEncoding.EncodeToString(want[q.Offset:end])}), nil
	}}
	got, err := ReadFile(context.Background(), backend, "source-session", "sandbox:/sandbox/report.pdf", int64(len(want)))
	if err != nil || !bytes.Equal(got, want) || calls < 2 {
		t.Fatalf("read: bytes=%d calls=%d err=%v", len(got), calls, err)
	}
}

func TestReadFileFailsClosed(t *testing.T) {
	for _, mode := range []string{"transport", "timeout", "exit", "truncated", "malformed", "empty", "too_large", "missing_id", "bad_base64", "short_chunk", "changed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			backend := &stubBackend{execFunc: func(ctx context.Context, req ExecRequest) (ExecResult, error) {
				q := decodeFileCommand(t, req.Command)
				calls++
				chunk := fileChunkResponse{Size: fileChunkBytes + 1, Fingerprint: "stable", Data: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, fileChunkBytes))}
				switch mode {
				case "transport":
					return ExecResult{}, errors.New("fixture transport error")
				case "timeout":
					return ExecResult{TimedOut: true}, nil
				case "exit":
					code := 1
					return ExecResult{ExitCode: &code, Stderr: "private file contents"}, nil
				case "truncated":
					r := fileResult(t, chunk)
					r.StdoutTruncated = true
					return r, nil
				case "malformed":
					r := fileResult(t, chunk)
					r.Stdout = "invalid"
					return r, nil
				case "empty":
					chunk.Size = 0
				case "too_large":
					chunk.Size = MaxFileBytes + 1
				case "missing_id":
					chunk.Fingerprint = ""
				case "bad_base64":
					chunk.Data = strings.Repeat("!", len(chunk.Data))
				case "short_chunk":
					chunk.Data = "AQ=="
				case "changed":
					if q.Offset > 0 {
						chunk.Fingerprint = "changed"
					}
				case "cancel":
					chunk.Size, chunk.Data = 1, "AQ=="
					cancel()
				}
				return fileResult(t, chunk), nil
			}}
			got, err := ReadFile(ctx, backend, "source-session", "/sandbox/output.bin", MaxFileBytes)
			if err == nil || got != nil || strings.Contains(err.Error(), "private file contents") {
				t.Fatalf("unsafe response: bytes=%d err=%v", len(got), err)
			}
			if mode == "cancel" && calls != 1 {
				t.Fatalf("read continued after cancellation: %d", calls)
			}
		})
	}
	backend := &stubBackend{execFunc: func(context.Context, ExecRequest) (ExecResult, error) {
		t.Fatal("unexpected remote read")
		return ExecResult{}, nil
	}}
	for _, tc := range []struct {
		session, path string
		limit         int64
	}{
		{"", "/sandbox/a", MaxFileBytes}, {"s", "/tmp/host", MaxFileBytes}, {"s", "/sandbox/a", 0}, {"s", "/sandbox/a", MaxFileBytes + 1},
	} {
		if _, err := ReadFile(context.Background(), backend, tc.session, tc.path, tc.limit); err == nil {
			t.Fatalf("invalid request accepted: %+v", tc)
		}
	}
	if _, err := ReadFile(context.Background(), nil, "s", "/sandbox/a", MaxFileBytes); !errors.Is(err, ErrSandboxDisabled) {
		t.Fatalf("disabled sandbox: %v", err)
	}
}

// The fixed reader runs in the Linux sandbox, including its Python fallback.
func TestFileReaderPythonProtocol(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sandbox file reader uses Linux directory descriptors")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable")
	}
	root := t.TempDir()
	inside := filepath.Join(root, "artifacts")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"json", "base64", "stat"} {
		if err := os.WriteFile(filepath.Join(inside, module+".py"), []byte("raise RuntimeError('artifact module must not be imported')"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	name := "chart '$(echo ignored).png"
	content := []byte("fixed artifact bytes")
	if err := os.WriteFile(filepath.Join(inside, name), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(inside, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(inside, "parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "empty"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	large, err := os.Create(filepath.Join(inside, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{name, "link", "parent/outside", "../outside", "empty", "large", "missing", "."} {
		q := fileChunkRequest{Root: inside, Path: file, Limit: MaxFileBytes}
		cmd := exec.Command("sh", "-c", fileReadCommand(q))
		cmd.Dir = inside
		cmd.Env = append(os.Environ(), "PYTHONPATH="+inside)
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("reader failed: %v", err)
		}
		var response fileChunkResponse
		if err := json.Unmarshal(output, &response); err != nil {
			t.Fatal(err)
		}
		if file == name {
			decoded, err := base64.StdEncoding.DecodeString(response.Data)
			if err != nil || !bytes.Equal(decoded, content) || response.Fingerprint == "" {
				t.Fatalf("reader response: %+v %v", response, err)
			}
		} else if response.Error == "" {
			t.Fatalf("unsafe file accepted: %q", file)
		}
	}
}
