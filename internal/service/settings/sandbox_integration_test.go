package settings_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/service/settings"
)

type integrationStubBackend struct {
	cfg       sandbox.Config
	execCalls atomic.Int32
}

func (s *integrationStubBackend) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	s.execCalls.Add(1)
	code := 0
	return sandbox.ExecResult{
		Stdout:   fmt.Sprintf("executed on %s with token %s", s.cfg.BaseURL, s.cfg.AuthToken),
		ExitCode: &code,
		Duration: time.Millisecond,
	}, nil
}

func (s *integrationStubBackend) Release(ctx context.Context, sessionID string) error {
	return nil
}

func (s *integrationStubBackend) Health(ctx context.Context) error {
	return nil
}

func TestSettingsService_DynamicBackend_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	initialContent := "SANDBOX_ENABLED=false\n" +
		"SANDBOX_BASE_URL=http://127.0.0.1:3874\n" +
		"SANDBOX_AUTH_TOKEN=initial-token\n" +
		"SANDBOX_SESSION_NAMESPACE=test-ns\n"

	if err := os.WriteFile(envPath, []byte(initialContent), 0600); err != nil {
		t.Fatalf("write initial .env: %v", err)
	}

	// Ensure clean process env before test
	t.Setenv("SANDBOX_ENABLED", "false")
	t.Setenv("SANDBOX_BASE_URL", "http://127.0.0.1:3874")
	t.Setenv("SANDBOX_AUTH_TOKEN", "initial-token")
	t.Setenv("SANDBOX_SESSION_NAMESPACE", "test-ns")

	cfgMgr := sandbox.NewConfigManager(sandbox.LoadConfigFromEnv())

	var lastCreatedStub atomic.Pointer[integrationStubBackend]
	var factoryCalls atomic.Int32

	factory := func(cfg sandbox.Config) sandbox.Backend {
		factoryCalls.Add(1)
		stub := &integrationStubBackend{cfg: cfg}
		lastCreatedStub.Store(stub)
		return stub
	}

	db := sandbox.NewDynamicBackend(cfgMgr.Get, factory)

	svc, err := settings.New(envPath)
	if err != nil {
		t.Fatalf("settings.New failed: %v", err)
	}
	svc.SetSandboxConfigManager(cfgMgr)

	ctx := context.Background()
	req := sandbox.ExecRequest{SessionID: "s1", Command: "echo hi"}

	// --- Phase 1: Disabled initially ---
	_, err = db.Exec(ctx, req)
	if !errors.Is(err, sandbox.ErrSandboxDisabled) {
		t.Fatalf("Phase 1: want ErrSandboxDisabled, got %v", err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("Phase 1: factory should not be called when disabled, got %d", factoryCalls.Load())
	}

	// --- Phase 2: Enable via UpdateEnvVar ---
	updateResp, err := svc.UpdateEnvVar(ctx, connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SANDBOX_ENABLED",
		Value: "true",
	}))
	if err != nil || !updateResp.Msg.GetSuccess() {
		t.Fatalf("Phase 2: UpdateEnvVar failed: %v, resp: %v", err, updateResp)
	}

	res, err := db.Exec(ctx, req)
	if err != nil {
		t.Fatalf("Phase 2: unexpected Exec error after enable: %v", err)
	}
	if res.Stdout != "executed on http://127.0.0.1:3874 with token initial-token" {
		t.Fatalf("Phase 2: unexpected stdout: %s", res.Stdout)
	}
	if factoryCalls.Load() != 1 {
		t.Fatalf("Phase 2: expected 1 factory call, got %d", factoryCalls.Load())
	}

	// --- Phase 3: Disable via UpdateEnvVar ---
	updateResp, err = svc.UpdateEnvVar(ctx, connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SANDBOX_ENABLED",
		Value: "false",
	}))
	if err != nil || !updateResp.Msg.GetSuccess() {
		t.Fatalf("Phase 3: UpdateEnvVar disable failed: %v", err)
	}

	_, err = db.Exec(ctx, req)
	if !errors.Is(err, sandbox.ErrSandboxDisabled) {
		t.Fatalf("Phase 3: want ErrSandboxDisabled after disable, got %v", err)
	}

	// --- Phase 4: Re-enable and then DeleteEnvVar ---
	_, _ = svc.UpdateEnvVar(ctx, connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SANDBOX_ENABLED",
		Value: "true",
	}))
	delResp, err := svc.DeleteEnvVar(ctx, connect.NewRequest(&v1.DeleteEnvVarRequest{
		Key: "SANDBOX_ENABLED",
	}))
	if err != nil || !delResp.Msg.GetSuccess() {
		t.Fatalf("Phase 4: DeleteEnvVar failed: %v", err)
	}
	_, err = db.Exec(ctx, req)
	if !errors.Is(err, sandbox.ErrSandboxDisabled) {
		t.Fatalf("Phase 4: want ErrSandboxDisabled after DeleteEnvVar, got %v", err)
	}

	// --- Phase 5: UpdateRawEnvFile with complete atomic snapshot ---
	newRawContent := "SANDBOX_ENABLED=true\n" +
		"SANDBOX_BASE_URL=http://127.0.0.1:9999\n" +
		"SANDBOX_AUTH_TOKEN=migrated-token\n" +
		"SANDBOX_SESSION_NAMESPACE=migrated-ns\n"

	rawResp, err := svc.UpdateRawEnvFile(ctx, connect.NewRequest(&v1.UpdateRawEnvFileRequest{
		Content: newRawContent,
	}))
	if err != nil || !rawResp.Msg.GetSuccess() {
		t.Fatalf("Phase 5: UpdateRawEnvFile failed: %v, resp: %v", err, rawResp)
	}

	res, err = db.Exec(ctx, req)
	if err != nil {
		t.Fatalf("Phase 5: unexpected Exec error after raw update: %v", err)
	}
	if res.Stdout != "executed on http://127.0.0.1:9999 with token migrated-token" {
		t.Fatalf("Phase 5: unexpected stdout: %s", res.Stdout)
	}
	// Verify in-process env sync
	if os.Getenv("SANDBOX_BASE_URL") != "http://127.0.0.1:9999" {
		t.Fatalf("Phase 5: in-process SANDBOX_BASE_URL not synced: %s", os.Getenv("SANDBOX_BASE_URL"))
	}
	if os.Getenv("SANDBOX_AUTH_TOKEN") != "migrated-token" {
		t.Fatalf("Phase 5: in-process SANDBOX_AUTH_TOKEN not synced: %s", os.Getenv("SANDBOX_AUTH_TOKEN"))
	}

	// --- Phase 6: UpdateRawEnvFile removing SANDBOX_ENABLED unsets env and disables ---
	rawWithoutEnabled := "SANDBOX_BASE_URL=http://127.0.0.1:9999\n" +
		"SANDBOX_AUTH_TOKEN=migrated-token\n" +
		"SANDBOX_SESSION_NAMESPACE=migrated-ns\n"

	rawResp, err = svc.UpdateRawEnvFile(ctx, connect.NewRequest(&v1.UpdateRawEnvFileRequest{
		Content: rawWithoutEnabled,
	}))
	if err != nil || !rawResp.Msg.GetSuccess() {
		t.Fatalf("Phase 6: UpdateRawEnvFile failed: %v", err)
	}

	_, err = db.Exec(ctx, req)
	if !errors.Is(err, sandbox.ErrSandboxDisabled) {
		t.Fatalf("Phase 6: want ErrSandboxDisabled after removing from raw, got %v", err)
	}
	if os.Getenv("SANDBOX_ENABLED") != "" {
		t.Fatalf("Phase 6: SANDBOX_ENABLED should be unset, got %q", os.Getenv("SANDBOX_ENABLED"))
	}

	// --- Phase 7: Protection against mixed endpoint+credential in single-key updates ---
	// Re-enable via raw file with known endpoint A and token A
	rawA := "SANDBOX_ENABLED=true\n" +
		"SANDBOX_BASE_URL=http://gateway-a.internal\n" +
		"SANDBOX_AUTH_TOKEN=token-for-a\n" +
		"SANDBOX_SESSION_NAMESPACE=ns-a\n"
	_, _ = svc.UpdateRawEnvFile(ctx, connect.NewRequest(&v1.UpdateRawEnvFileRequest{Content: rawA}))

	res, err = db.Exec(ctx, req)
	if err != nil {
		t.Fatalf("Phase 7 setup: unexpected error: %v", err)
	}
	if res.Stdout != "executed on http://gateway-a.internal with token token-for-a" {
		t.Fatalf("Phase 7 setup: unexpected stdout: %s", res.Stdout)
	}

	// User calls UpdateEnvVar on SANDBOX_BASE_URL (RequiresRestart: true).
	// Because single-field update requires restart and cannot atomically update the token,
	// the active running ConfigManager snapshot must NOT expose token-for-a to gateway-b.internal!
	_, err = svc.UpdateEnvVar(ctx, connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SANDBOX_BASE_URL",
		Value: "http://gateway-b.internal",
	}))
	if err != nil {
		t.Fatalf("Phase 7: UpdateEnvVar SANDBOX_BASE_URL failed: %v", err)
	}

	// DynamicBackend must NOT have switched to (gateway-b, token-for-a)
	res, err = db.Exec(ctx, req)
	if err != nil {
		t.Fatalf("Phase 7: unexpected error: %v", err)
	}
	if res.Stdout == "executed on http://gateway-b.internal with token token-for-a" {
		t.Fatal("CRITICAL SAFETY VIOLATION: credential token-for-a was leaked to gateway-b.internal in mixed configuration window!")
	}
	// Active backend safely continues using verified gateway-a config
	if res.Stdout != "executed on http://gateway-a.internal with token token-for-a" {
		t.Fatalf("Phase 7: expected continued safe execution on gateway-a, got: %s", res.Stdout)
	}

	// --- Phase 8: Invalid raw .env content is rejected without corrupting state ---
	invalidResp, err := svc.UpdateRawEnvFile(ctx, connect.NewRequest(&v1.UpdateRawEnvFileRequest{
		Content: "MALFORMED_LINE=\"unterminated quote",
	}))
	if err != nil {
		t.Fatalf("Phase 8: unexpected RPC error: %v", err)
	}
	if invalidResp.Msg.GetSuccess() {
		t.Fatal("Phase 8: expected malformed raw .env content to be rejected")
	}

	// Active backend remains completely intact and functional
	res, err = db.Exec(ctx, req)
	if err != nil {
		t.Fatalf("Phase 8: backend should remain functional after rejected raw update: %v", err)
	}
	if res.Stdout != "executed on http://gateway-a.internal with token token-for-a" {
		t.Fatalf("Phase 8: unexpected stdout: %s", res.Stdout)
	}
}
