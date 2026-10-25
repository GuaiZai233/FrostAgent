package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type contentExecutor struct{ cache *ContentCache }

func (e *contentExecutor) Validate(Request) error { return nil }
func (e *contentExecutor) Execute(_ context.Context, r Request) (Outcome, error) {
	err := e.cache.Put(r, "synthetic-private-source", []byte("synthetic-image"))
	return Outcome{State: Succeeded, Code: "completed"}, err
}
func (e *contentExecutor) Content(id string) (Content, bool) { return e.cache.Content(id) }
func (e *contentExecutor) Close() error                      { return e.cache.Close() }
func TestContentIsTransientAuthenticatedBoundedAndFenced(t *testing.T) {
	executor := &contentExecutor{NewContentCache()}
	s, cfg := testService(t, executor)
	server := httptest.NewTLSServer(testHandler(t, s))
	defer server.Close()
	client := testClient(t, server)
	req := testRequest("synthetic-content")
	if _, err := client.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	value, err := client.Content(context.Background(), req)
	if err != nil || value.Text != "synthetic-private-source" {
		t.Fatal("content unavailable", err)
	}
	bad := req
	bad.Generation++
	if _, err := client.Content(context.Background(), bad); !errors.Is(err, ErrProtocol) {
		t.Fatal("content crossed generation")
	}
	client.config.Token = "wrong"
	if _, err := client.Content(context.Background(), req); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("content bypassed auth")
	}
	for _, dir := range []string{cfg.StateDir, cfg.AuditDir} {
		_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				data, _ := os.ReadFile(path)
				if strings.Contains(string(data), "synthetic-private-source") || strings.Contains(string(data), "synthetic-image") {
					t.Error("raw content persisted")
				}
			}
			return err
		})
	}
	cache := executor.cache
	firstTimer := cache.timer
	for i := 0; i < 300; i++ {
		r := testRequest(fmt.Sprintf("content-%d", i))
		if err := cache.Put(r, "synthetic", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.entries) > 128 || cache.timer != firstTimer {
		t.Fatal("cache/timer grows per operation")
	}
	cache.mu.Lock()
	cache.now = func() time.Time { return time.Now().Add(time.Hour) }
	cache.mu.Unlock()
	if _, ok := cache.Content("content-299"); ok {
		t.Fatal("expired content retained")
	}
	if cache.bytes != 0 {
		t.Fatal("expired bytes not released")
	}
}

type controlExecutor struct{ started, release chan struct{} }

func (e controlExecutor) Validate(Request) error { return nil }
func (e controlExecutor) Execute(_ context.Context, r Request) (Outcome, error) {
	if r.Kind == "terminal.exec" {
		close(e.started)
		<-e.release
	}
	return Outcome{State: Succeeded, Code: "completed"}, nil
}
func TestTakeoverHasReservedCapacityAndFactoryRequiresLease(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.MaxConcurrent = 1
	started, release := make(chan struct{}), make(chan struct{})
	s, err := Open(cfg, controlExecutor{started, release})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	defer close(release)
	called := false
	if _, err := OpenWithFactory(cfg, func() (Executor, error) { called = true; return testExecutor{}, nil }); err == nil || called {
		t.Fatal("registry factory ran before exclusive lease")
	}
	if _, err := s.Submit(context.Background(), testRequest("long-op")); err != nil {
		t.Fatal(err)
	}
	<-started
	req := testRequest("takeover")
	req.Kind = "desktop.takeover"
	if _, err := s.Submit(context.Background(), req); err != nil {
		t.Fatal("takeover blocked behind full terminal capacity", err)
	}
	if result := awaitOperation(t, s, req.OperationID); result.State != Succeeded {
		t.Fatal(result)
	}
}
