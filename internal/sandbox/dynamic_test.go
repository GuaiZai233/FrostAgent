package sandbox

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type stubBackend struct {
	execFunc    func(ctx context.Context, req ExecRequest) (ExecResult, error)
	releaseFunc func(ctx context.Context, sessionID string) error
	healthFunc  func(ctx context.Context) error
}

func (s *stubBackend) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if s.execFunc != nil {
		return s.execFunc(ctx, req)
	}
	code := 0
	return ExecResult{ExitCode: &code, Duration: time.Millisecond}, nil
}

func (s *stubBackend) Release(ctx context.Context, sessionID string) error {
	if s.releaseFunc != nil {
		return s.releaseFunc(ctx, sessionID)
	}
	return nil
}

func (s *stubBackend) Health(ctx context.Context) error {
	if s.healthFunc != nil {
		return s.healthFunc(ctx)
	}
	return nil
}

func validTestConfig() Config {
	return Config{
		Enabled:          true,
		BaseURL:          "http://127.0.0.1:3874",
		AuthToken:        "test-token",
		SessionNamespace: "test",
		ClientTimeout:    135 * time.Second,
	}
}

func TestDynamicBackend_DisabledReturnsError(t *testing.T) {
	db := NewDynamicBackend(
		func() Config { return Config{Enabled: false} },
		func(cfg Config) Backend {
			t.Fatal("factory must not be called when disabled")
			return nil
		},
	)

	_, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo hi"})
	if !errors.Is(err, ErrSandboxDisabled) {
		t.Fatalf("Exec: want ErrSandboxDisabled, got %v", err)
	}

	if err := db.Health(context.Background()); !errors.Is(err, ErrSandboxDisabled) {
		t.Fatalf("Health: want ErrSandboxDisabled, got %v", err)
	}

	if err := db.Release(context.Background(), "s1"); !errors.Is(err, ErrSandboxDisabled) {
		t.Fatalf("Release: want ErrSandboxDisabled, got %v", err)
	}
}

func TestDynamicBackend_InvalidConfigReturnsError(t *testing.T) {
	cfg := Config{
		Enabled:          true,
		BaseURL:          "http://127.0.0.1:3874",
		AuthToken:        "",
		SessionNamespace: "test",
	}
	db := NewDynamicBackend(
		func() Config { return cfg },
		func(c Config) Backend {
			t.Fatal("factory must not be called with invalid config")
			return nil
		},
	)

	_, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo"})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
	if errors.Is(err, ErrSandboxDisabled) {
		t.Fatal("should not be ErrSandboxDisabled for invalid config")
	}
}

func TestDynamicBackend_DelegatesWhenEnabled(t *testing.T) {
	var called atomic.Bool
	stub := &stubBackend{
		execFunc: func(_ context.Context, req ExecRequest) (ExecResult, error) {
			called.Store(true)
			code := 0
			return ExecResult{Stdout: "ok", ExitCode: &code, Duration: time.Millisecond}, nil
		},
	}

	db := NewDynamicBackend(
		func() Config { return validTestConfig() },
		func(cfg Config) Backend { return stub },
	)

	result, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called.Load() {
		t.Fatal("stub backend was not called")
	}
	if result.Stdout != "ok" {
		t.Fatalf("got stdout %q, want %q", result.Stdout, "ok")
	}
}

func TestDynamicBackend_CachesBackend(t *testing.T) {
	var factoryCalls atomic.Int32
	stub := &stubBackend{}

	db := NewDynamicBackend(
		func() Config { return validTestConfig() },
		func(cfg Config) Backend {
			factoryCalls.Add(1)
			return stub
		},
	)

	for range 5 {
		if _, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo"}); err != nil {
			t.Fatal(err)
		}
	}

	if n := factoryCalls.Load(); n != 1 {
		t.Fatalf("factory called %d times, want 1", n)
	}
}

func TestDynamicBackend_RecreatesOnConfigChange(t *testing.T) {
	var factoryCalls atomic.Int32
	stub := &stubBackend{}

	cfgA := validTestConfig()
	cfgB := validTestConfig()
	cfgB.BaseURL = "http://127.0.0.1:9999"

	current := cfgA
	db := NewDynamicBackend(
		func() Config { return current },
		func(cfg Config) Backend {
			factoryCalls.Add(1)
			return stub
		},
	)

	if _, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo"}); err != nil {
		t.Fatal(err)
	}
	if n := factoryCalls.Load(); n != 1 {
		t.Fatalf("after first call: factory called %d times, want 1", n)
	}

	current = cfgB
	if _, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo"}); err != nil {
		t.Fatal(err)
	}
	if n := factoryCalls.Load(); n != 2 {
		t.Fatalf("after config change: factory called %d times, want 2", n)
	}
}

func TestDynamicBackend_EnableAfterDisabled(t *testing.T) {
	stub := &stubBackend{}
	enabled := false

	db := NewDynamicBackend(
		func() Config {
			if enabled {
				return validTestConfig()
			}
			return Config{Enabled: false}
		},
		func(cfg Config) Backend { return stub },
	)

	_, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo"})
	if !errors.Is(err, ErrSandboxDisabled) {
		t.Fatalf("want ErrSandboxDisabled, got %v", err)
	}

	enabled = true
	result, err := db.Exec(context.Background(), ExecRequest{SessionID: "s1", Command: "echo"})
	if err != nil {
		t.Fatalf("after enable: unexpected error: %v", err)
	}
	if result.ExitCode == nil {
		t.Fatal("after enable: ExitCode is nil")
	}
}

func TestDynamicBackend_HealthDelegates(t *testing.T) {
	var called atomic.Bool
	stub := &stubBackend{
		healthFunc: func(_ context.Context) error {
			called.Store(true)
			return nil
		},
	}

	db := NewDynamicBackend(
		func() Config { return validTestConfig() },
		func(cfg Config) Backend { return stub },
	)

	if err := db.Health(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called.Load() {
		t.Fatal("health was not delegated to stub")
	}
}

func TestDynamicBackend_ReleaseDelegates(t *testing.T) {
	var called atomic.Bool
	stub := &stubBackend{
		releaseFunc: func(_ context.Context, sessionID string) error {
			called.Store(true)
			if sessionID != "s1" {
				t.Fatalf("got sessionID %q, want %q", sessionID, "s1")
			}
			return nil
		},
	}

	db := NewDynamicBackend(
		func() Config { return validTestConfig() },
		func(cfg Config) Backend { return stub },
	)

	if err := db.Release(context.Background(), "s1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called.Load() {
		t.Fatal("release was not delegated to stub")
	}
}

func TestConfigManager_LifecycleAndConcurrency(t *testing.T) {
	initial := validTestConfig()
	initial.Enabled = false
	mgr := NewConfigManager(initial)

	// Initial state
	if mgr.Get().Enabled {
		t.Fatal("expected initially disabled")
	}

	// SetEnabled
	mgr.SetEnabled(true)
	if !mgr.Get().Enabled {
		t.Fatal("expected enabled after SetEnabled(true)")
	}

	// ApplySnapshot replaces everything atomically
	newCfg := Config{
		Enabled:          true,
		BaseURL:          "http://127.0.0.1:9999",
		AuthToken:        "new-secret",
		SessionNamespace: "new-ns",
		ClientTimeout:    100 * time.Second,
	}
	mgr.ApplySnapshot(newCfg)
	got := mgr.Get()
	if got.BaseURL != "http://127.0.0.1:9999" || got.AuthToken != "new-secret" || got.SessionNamespace != "new-ns" {
		t.Fatalf("unexpected config snapshot: %+v", got)
	}

	// Concurrent reads and writes
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			if i%2 == 0 {
				mgr.SetEnabled(true)
			} else {
				mgr.SetEnabled(false)
			}
		}
	}()

	for range 100 {
		_ = mgr.Get()
	}
	<-done
}

func TestLoadConfigFromMap(t *testing.T) {
	// Empty map uses defaults
	emptyCfg := LoadConfigFromMap(map[string]string{})
	if emptyCfg.Enabled {
		t.Error("expected default Enabled=false")
	}
	if emptyCfg.BaseURL != DefaultBaseURL {
		t.Errorf("expected default BaseURL %s, got %s", DefaultBaseURL, emptyCfg.BaseURL)
	}
	if emptyCfg.SessionNamespace != DefaultSessionNamespace {
		t.Errorf("expected default SessionNamespace %s, got %s", DefaultSessionNamespace, emptyCfg.SessionNamespace)
	}

	// Populated map
	m := map[string]string{
		"SANDBOX_ENABLED":           "true",
		"SANDBOX_BASE_URL":          "https://sandbox.corp.net",
		"SANDBOX_AUTH_TOKEN":        "tok-12345",
		"SANDBOX_SESSION_NAMESPACE": "corp-ns",
	}
	cfg := LoadConfigFromMap(m)
	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.BaseURL != "https://sandbox.corp.net" {
		t.Errorf("got BaseURL %s, want https://sandbox.corp.net", cfg.BaseURL)
	}
	if cfg.AuthToken != "tok-12345" {
		t.Errorf("got AuthToken %s, want tok-12345", cfg.AuthToken)
	}
	if cfg.SessionNamespace != "corp-ns" {
		t.Errorf("got SessionNamespace %s, want corp-ns", cfg.SessionNamespace)
	}
}
