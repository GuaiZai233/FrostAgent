package codeinterpreter_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox/codeinterpreter"
)

// Exercise the real HTTP client and the gateway's per-worker execution lock.
// Linux runs the actual Python writer; other platforms simulate 30 ms execution.
// A sequential 32 KiB transfer needs at least 144 seconds at this network latency.
func TestWriteFileMaximumWithRemoteLatency(t *testing.T) {
	actualWriter := runtime.GOOS == "linux"
	if actualWriter {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Fatal("Linux protocol test requires python3")
		}
	}
	root := t.TempDir()
	data := make([]byte, sandbox.MaxFileBytes)
	_, _ = rand.New(rand.NewSource(1)).Read(data)
	var mu sync.Mutex
	var staged []byte
	var stage string
	committed := false
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" {
			w.WriteHeader(http.StatusCreated)
			return
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		var payload struct{ Command, Cwd string }
		if r.URL.Path != "/api/v1/shell/exec" || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Command) > sandbox.MaxCommandLength || payload.Cwd != "/sandbox" {
			t.Error("invalid upload transport request")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		i := strings.LastIndex(payload.Command, " '")
		if i < 0 || !strings.HasSuffix(payload.Command, "'") {
			t.Error("missing upload request")
			http.Error(w, "invalid command", http.StatusBadRequest)
			return
		}
		encoded, err := base64.StdEncoding.DecodeString(payload.Command[i+2 : len(payload.Command)-1])
		var q struct {
			Op, Path, Stage, Data, Hash string
			Offset                      int
			Size                        int64
		}
		if err != nil || json.Unmarshal(encoded, &q) != nil || q.Size != int64(len(data)) || q.Path != "received/large.bin" {
			t.Error("invalid upload descriptor")
			http.Error(w, "invalid descriptor", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if actualWriter {
			// Keep the production command and protocol, changing only the root
			// to the test sandbox. The lock matches ShellExecutor.execute.
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(encoded, &raw)
			raw["root"], _ = json.Marshal(root)
			encoded, _ = json.Marshal(raw)
			command := payload.Command[:i+2] + base64.StdEncoding.EncodeToString(encoded) + "'"
			output, err := exec.CommandContext(r.Context(), "sh", "-c", command).CombinedOutput()
			if err != nil {
				t.Errorf("writer execution failed: %v: %s", err, output)
				http.Error(w, "execution failed", http.StatusInternalServerError)
				return
			}
			if q.Op == "commit" {
				committed = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"stdout": string(output), "exit_code": 0, "timed_out": false})
			return
		}
		time.Sleep(30 * time.Millisecond)
		if q.Op == "begin" {
			if staged != nil || q.Offset != 0 {
				t.Error("upload initialized twice")
			}
			staged, stage = make([]byte, len(data)), q.Stage
		}
		if stage != q.Stage {
			t.Error("upload identity changed")
		}
		switch q.Op {
		case "begin", "write":
			part, err := base64.StdEncoding.DecodeString(q.Data)
			if err != nil || q.Offset < 0 || q.Offset+len(part) > len(staged) {
				t.Error("invalid upload offset")
				http.Error(w, "invalid chunk", http.StatusBadRequest)
				return
			}
			copy(staged[q.Offset:], part)
		case "commit":
			hash := sha256.Sum256(staged)
			if !bytes.Equal(staged, data) || q.Hash != hex.EncodeToString(hash[:]) {
				t.Error("upload contents changed or chunks are missing")
			}
			committed = true
		case "abort":
			staged = nil
		default:
			t.Errorf("unexpected upload operation: %s", q.Op)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"stdout": `{"ok":true}`, "exit_code": 0, "timed_out": false})
	}))
	defer server.Close()
	client := codeinterpreter.New(sandbox.Config{BaseURL: server.URL, AuthToken: "fixture-token"}, codeinterpreter.WithHTTPClient(server.Client()))
	// Leave 30 seconds of the shared preparation budget for download and health
	// checks, while allowing actual serialized Python startup on slower CI runners.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	started := time.Now()
	if err := sandbox.WriteFile(ctx, client, "attachment-latency", "/sandbox/received/large.bin", data); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !committed || peak.Load() < 2 || peak.Load() > 8 {
		t.Fatalf("committed=%v peak requests=%d", committed, peak.Load())
	}
	if actualWriter {
		got, err := os.ReadFile(filepath.Join(root, "received", "large.bin"))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("actual writer lost data: %v", err)
		}
	}
	t.Logf("30 MiB with 150 ms request latency: %s, peak requests=%d", time.Since(started), peak.Load())
}
