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

func TestSettingsService_ExternalEnvPrecedence_Regression(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	// Initial .env file contains BOT_NAME and sandbox endpoint settings,
	// but does NOT contain UPSTREAM_API_KEY or SANDBOX_AUTH_TOKEN.
	initialContent := "BOT_NAME=OriginalBot\n" +
		"SANDBOX_ENABLED=true\n" +
		"SANDBOX_BASE_URL=http://127.0.0.1:3874\n"
	if err := os.WriteFile(envPath, []byte(initialContent), 0600); err != nil {
		t.Fatalf("write initial .env: %v", err)
	}

	// Simulate external environment injection (e.g. Docker, systemd, Kubernetes secrets).
	const externalUpstreamSecret = "external-upstream-prod-secret"
	const externalSandboxSecret = "external-sandbox-token-secret"
	t.Setenv("UPSTREAM_API_KEY", externalUpstreamSecret)
	t.Setenv("SANDBOX_AUTH_TOKEN", externalSandboxSecret)
	t.Setenv("BOT_NAME", "OriginalBot")
	t.Setenv("SANDBOX_ENABLED", "true")
	t.Setenv("SANDBOX_BASE_URL", "http://127.0.0.1:3874")

	cfgMgr := sandbox.NewConfigManager(sandbox.LoadConfigFromEnv())
	if cfgMgr.Get().AuthToken != externalSandboxSecret {
		t.Fatalf("initial snapshot should have external token, got %q", cfgMgr.Get().AuthToken)
	}

	svc, err := settings.New(envPath)
	if err != nil {
		t.Fatalf("settings.New failed: %v", err)
	}
	svc.SetSandboxConfigManager(cfgMgr)

	db := sandbox.NewDynamicBackend(cfgMgr.Get, func(cfg sandbox.Config) sandbox.Backend {
		return &integrationStubBackend{cfg: cfg}
	})

	ctx := context.Background()
	req := sandbox.ExecRequest{SessionID: "s1", Command: "echo test"}

	// Initial execution succeeds using externally injected token
	res, err := db.Exec(ctx, req)
	if err != nil {
		t.Fatalf("initial Exec failed: %v", err)
	}
	expectedOutput := fmt.Sprintf("executed on http://127.0.0.1:3874 with token %s", externalSandboxSecret)
	if res.Stdout != expectedOutput {
		t.Fatalf("want %q, got %q", expectedOutput, res.Stdout)
	}

	// Step 1: Admin edits unrelated key (BOT_NAME) in Raw .env editor and saves.
	unrelatedRawUpdate := "BOT_NAME=UpdatedBot\n" +
		"SANDBOX_ENABLED=true\n" +
		"SANDBOX_BASE_URL=http://127.0.0.1:3874\n"
	rawResp, err := svc.UpdateRawEnvFile(ctx, connect.NewRequest(&v1.UpdateRawEnvFileRequest{
		Content: unrelatedRawUpdate,
	}))
	if err != nil || !rawResp.Msg.GetSuccess() {
		t.Fatalf("UpdateRawEnvFile failed: %v, resp: %v", err, rawResp)
	}

	// Verify intended edit took effect
	if os.Getenv("BOT_NAME") != "UpdatedBot" {
		t.Fatalf("expected BOT_NAME to be UpdatedBot, got %q", os.Getenv("BOT_NAME"))
	}

	// Verify externally injected secrets NOT present in .env were NOT unset or wiped!
	if os.Getenv("UPSTREAM_API_KEY") != externalUpstreamSecret {
		t.Fatalf("UPSTREAM_API_KEY was corrupted or unset: want %q, got %q", externalUpstreamSecret, os.Getenv("UPSTREAM_API_KEY"))
	}
	if os.Getenv("SANDBOX_AUTH_TOKEN") != externalSandboxSecret {
		t.Fatalf("SANDBOX_AUTH_TOKEN was corrupted or unset: want %q, got %q", externalSandboxSecret, os.Getenv("SANDBOX_AUTH_TOKEN"))
	}

	// Verify runtime sandbox snapshot retained the external secret as effective configuration
	if cfgMgr.Get().AuthToken != externalSandboxSecret {
		t.Fatalf("sandbox snapshot token was corrupted or wiped: want %q, got %q", externalSandboxSecret, cfgMgr.Get().AuthToken)
	}
	res, err = db.Exec(ctx, req)
	if err != nil {
		t.Fatalf("Exec failed after raw update: %v", err)
	}
	if res.Stdout != expectedOutput {
		t.Fatalf("Exec should continue with external token: want %q, got %q", expectedOutput, res.Stdout)
	}

	// Step 2: If raw .env contains a value for an externally overridden key,
	// the external environment retains precedence in process environment.
	conflictRawUpdate := "BOT_NAME=UpdatedBot\n" +
		"UPSTREAM_API_KEY=file-should-not-override\n" +
		"SANDBOX_ENABLED=true\n" +
		"SANDBOX_BASE_URL=http://127.0.0.1:3874\n"
	rawResp, err = svc.UpdateRawEnvFile(ctx, connect.NewRequest(&v1.UpdateRawEnvFileRequest{
		Content: conflictRawUpdate,
	}))
	if err != nil || !rawResp.Msg.GetSuccess() {
		t.Fatalf("conflict UpdateRawEnvFile failed: %v", err)
	}
	if os.Getenv("UPSTREAM_API_KEY") != externalUpstreamSecret {
		t.Fatalf("external UPSTREAM_API_KEY should retain precedence over .env file: want %q, got %q", externalUpstreamSecret, os.Getenv("UPSTREAM_API_KEY"))
	}

	// Step 3: Variables owned by .env (not externally overridden, like BOT_NAME) DO get unset when removed.
	rawWithoutBotName := "SANDBOX_ENABLED=true\n" +
		"SANDBOX_BASE_URL=http://127.0.0.1:3874\n"
	rawResp, err = svc.UpdateRawEnvFile(ctx, connect.NewRequest(&v1.UpdateRawEnvFileRequest{
		Content: rawWithoutBotName,
	}))
	if err != nil || !rawResp.Msg.GetSuccess() {
		t.Fatalf("rawWithoutBotName failed: %v", err)
	}
	if os.Getenv("BOT_NAME") != "" {
		t.Fatalf("BOT_NAME should be unset after removal from .env, got %q", os.Getenv("BOT_NAME"))
	}
	// While external secrets remain intact
	if os.Getenv("UPSTREAM_API_KEY") != externalUpstreamSecret {
		t.Fatalf("UPSTREAM_API_KEY should still exist: want %q, got %q", externalUpstreamSecret, os.Getenv("UPSTREAM_API_KEY"))
	}
	if os.Getenv("SANDBOX_AUTH_TOKEN") != externalSandboxSecret {
		t.Fatalf("SANDBOX_AUTH_TOKEN should still exist: want %q, got %q", externalSandboxSecret, os.Getenv("SANDBOX_AUTH_TOKEN"))
	}
}
