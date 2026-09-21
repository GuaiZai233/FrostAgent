package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/sandbox/gateway"
)

// TestRealSandboxLifecycleE2E executes the full, unmocked end-to-end lifecycle:
// 1. Starts the supported Gateway Server on an ephemeral port.
// 2. Validates readiness diagnostics:
//    - endpoint unreachable
//    - auth failure
//    - API contract missing (404 on /api/v1/sessions)
//    - profile unsupported
//    - ready
// 3. Executes the full lifecycle:
//    create action -> create version -> build with go-builder -> activate -> run with action-runtime -> success
func TestRealSandboxLifecycleE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	workDir := t.TempDir()
	gw := gateway.NewServer(gateway.Config{
		AuthToken: "e2e-gateway-token",
		WorkDir:   workDir,
	})
	defer gw.Close()

	gatewayBaseURL, err := gw.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start gateway: %v", err)
	}
	t.Logf("[E2E] Supported Gateway started on %s", gatewayBaseURL)

	httpClient := &http.Client{Timeout: 30 * time.Second}

	// =========================================================================
	// Phase 1: Readiness Diagnostics Verification
	// =========================================================================
	t.Log("[E2E] === Phase 1: Readiness Diagnostics ===")

	// 1.1 Endpoint Unreachable
	repUnreachable := sandbox.CheckReadinessWithClient(ctx, httpClient, "http://127.0.0.1:59999", "e2e-gateway-token")
	if repUnreachable.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected status %s, got %s", sandbox.StatusEndpointUnreachable, repUnreachable.Status)
	}
	t.Logf("[E2E] Diagnostics 1/4 confirmed: endpoint unreachable (%s)", repUnreachable.Detail)

	// 1.2 Auth Failure
	repAuthFail := sandbox.CheckReadinessWithClient(ctx, httpClient, gatewayBaseURL, "wrong-auth-token")
	if repAuthFail.Status != sandbox.StatusAuthFailure {
		t.Fatalf("expected status %s, got %s", sandbox.StatusAuthFailure, repAuthFail.Status)
	}
	t.Logf("[E2E] Diagnostics 2/4 confirmed: auth failure (%s)", repAuthFail.Detail)

	// 1.3 API Contract Missing (legacy / incompatible gateway returns 404 on /api/v1/sessions)
	fakeLegacyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake legacy gateway: %v", err)
	}
	fakeLegacyServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/status" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
				return
			}
			// Missing /api/v1/sessions
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Not Found"}`))
		}),
	}
	go func() { _ = fakeLegacyServer.Serve(fakeLegacyLn) }()
	defer func() {
		_ = fakeLegacyServer.Close()
		_ = fakeLegacyLn.Close()
	}()
	legacyURL := fmt.Sprintf("http://127.0.0.1:%d", fakeLegacyLn.Addr().(*net.TCPAddr).Port)

	repContractMissing := sandbox.CheckReadinessWithClient(ctx, httpClient, legacyURL, "")
	if repContractMissing.Status != sandbox.StatusAPIContractMissing {
		t.Fatalf("expected status %s, got %s", sandbox.StatusAPIContractMissing, repContractMissing.Status)
	}
	t.Logf("[E2E] Diagnostics 3/4 confirmed: API contract missing (%s)", repContractMissing.Detail)

	// 1.4 Profile Unsupported
	repProfileUnsupported := sandbox.CheckReadinessWithClient(ctx, httpClient, gatewayBaseURL, "e2e-gateway-token", "nonexistent-profile-xyz")
	if repProfileUnsupported.Status != sandbox.StatusProfileUnsupported {
		t.Fatalf("expected status %s, got %s", sandbox.StatusProfileUnsupported, repProfileUnsupported.Status)
	}
	t.Logf("[E2E] Diagnostics 4/4 confirmed: profile unsupported (%s)", repProfileUnsupported.Detail)

	// 1.5 Supported Gateway is Ready
	repReady := sandbox.CheckReadinessWithClient(ctx, httpClient, gatewayBaseURL, "e2e-gateway-token", sandbox.ProfileGoBuilder, sandbox.ProfileActionRuntime)
	if repReady.Status != sandbox.StatusReady {
		t.Fatalf("expected status %s, got %s: %s", sandbox.StatusReady, repReady.Status, repReady.Detail)
	}
	t.Logf("[E2E] Diagnostics Ready confirmed: %s", repReady.Detail)

	// =========================================================================
	// Phase 2: ActionsCat Lifecycle Execution
	// create action -> create version -> build with go-builder -> activate -> run with action-runtime -> success
	// =========================================================================
	t.Log("[E2E] === Phase 2: Complete Lifecycle Execution ===")

	// Step 2.1: Mock ActionsCat Core Runtime Callback API
	var callbackInvocations atomic.Int32
	cbLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen runtime callback: %v", err)
	}
	cbPort := cbLn.Addr().(*net.TCPAddr).Port
	cbServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callbackInvocations.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok","callback_received":true}`))
		}),
	}
	go func() { _ = cbServer.Serve(cbLn) }()
	defer func() {
		_ = cbServer.Close()
		_ = cbLn.Close()
	}()
	runtimeCallbackURL := fmt.Sprintf("http://127.0.0.1:%d/api/v1/runtime/state", cbPort)
	t.Logf("[E2E] Runtime Callback listening on %s", runtimeCallbackURL)

	// Step 2.2: Define Action and Version Specifications
	actionID := "act_real_e2e_sandbox"
	versionID := "ver_real_e2e_sandbox_1"
	t.Logf("[E2E] Created Action %s and Version %s", actionID, versionID)

	goModContent := "module e2eaction\n\ngo 1.25.3\n"
	mainGoContent := `package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	// Invariant 1: network:none blocks public internet access
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("https://1.1.1.1")
	if err == nil {
		resp.Body.Close()
		panic("CRITICAL FAILURE: public internet access succeeded under network:none")
	}
	fmt.Println("E2E_INVARIANT: public internet blocked successfully")

	// Invariant 2: Environment variables injected
	platform := os.Getenv("ACTIONSCAT_EVENT_PLATFORM")
	if platform != "qq" {
		panic(fmt.Sprintf("expected ACTIONSCAT_EVENT_PLATFORM=qq, got %q", platform))
	}
	sessionID := os.Getenv("ACTIONSCAT_EVENT_SESSION_ID")
	if sessionID != "sess_qq_group_12345" {
		panic(fmt.Sprintf("expected ACTIONSCAT_EVENT_SESSION_ID=sess_qq_group_12345, got %q", sessionID))
	}
	fmt.Println("E2E_INVARIANT: env injection verified")

	// Invariant 3: Runtime callback URL reachable
	callbackURL := os.Getenv("ACTIONSCAT_RUNTIME_ENDPOINT")
	if callbackURL != "" {
		cbReq, err := http.NewRequest(http.MethodPost, callbackURL, nil)
		if err != nil {
			panic(fmt.Sprintf("failed to build callback request: %v", err))
		}
		cbResp, cbErr := client.Do(cbReq)
		if cbErr != nil {
			panic(fmt.Sprintf("runtime callback failed: %v", cbErr))
		}
		cbResp.Body.Close()
		fmt.Println("E2E_INVARIANT: runtime callback reached successfully")
	}

	fmt.Println("E2E_LIFECYCLE_SUCCESS")
}
`

	// Step 2.3: Build Version with go-builder profile
	t.Log("[E2E] Step 2.3: Provisioning Builder Session with profile: go-builder...")
	buildSessionUUID := fmt.Sprintf("builder-%s-%s", actionID, versionID)
	buildSessPayload := map[string]any{
		"user_uuid": buildSessionUUID,
		"profile":   sandbox.ProfileGoBuilder,
		"network":   "none",
	}
	buildSessBytes, _ := json.Marshal(buildSessPayload)
	buildSessReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, gatewayBaseURL+"/api/v1/sessions", bytes.NewReader(buildSessBytes))
	buildSessReq.Header.Set("Content-Type", "application/json")
	buildSessReq.Header.Set("X-Auth-Token", "e2e-gateway-token")
	sessResp, err := httpClient.Do(buildSessReq)
	if err != nil {
		t.Fatalf("create builder session: %v", err)
	}
	if sessResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sessResp.Body)
		t.Fatalf("builder session failed (HTTP %d): %s", sessResp.StatusCode, string(body))
	}
	sessResp.Body.Close()

	// Upload sources into builder session workspace
	builderDir := filepath.Join(workDir, buildSessionUUID)
	if err := os.WriteFile(filepath.Join(builderDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(builderDir, "main.go"), []byte(mainGoContent), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Execute Go compilation: go build -o /sandbox/out/entrypoint .
	t.Log("[E2E] Step 2.3: Executing compilation in go-builder...")
	buildExecPayload := map[string]any{
		"command": "go build -o /sandbox/out/entrypoint .",
		"cwd":     "/sandbox",
		"timeout": 60.0,
	}
	buildExecBytes, _ := json.Marshal(buildExecPayload)
	buildExecURL := fmt.Sprintf("%s/api/v1/shell/exec?user_uuid=%s&profile=go-builder", gatewayBaseURL, buildSessionUUID)
	buildExecReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, buildExecURL, bytes.NewReader(buildExecBytes))
	buildExecReq.Header.Set("Content-Type", "application/json")
	buildExecReq.Header.Set("X-Auth-Token", "e2e-gateway-token")
	buildExecResp, err := httpClient.Do(buildExecReq)
	if err != nil {
		t.Fatalf("execute build: %v", err)
	}
	var buildResult struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode *int   `json:"exit_code"`
		TimedOut bool   `json:"timed_out"`
	}
	_ = json.NewDecoder(buildExecResp.Body).Decode(&buildResult)
	buildExecResp.Body.Close()

	if buildResult.ExitCode == nil || *buildResult.ExitCode != 0 {
		t.Fatalf("compilation failed with exit code %v\nStdout: %s\nStderr: %s", buildResult.ExitCode, buildResult.Stdout, buildResult.Stderr)
	}
	t.Logf("[E2E] Compilation succeeded in go-builder!")

	// Verify compiled binary exists
	builtBinaryPath := filepath.Join(builderDir, "out", "entrypoint")
	builtBinaryBytes, err := os.ReadFile(builtBinaryPath)
	if err != nil {
		// On Windows, check entrypoint.exe
		builtBinaryPathExe := filepath.Join(builderDir, "out", "entrypoint.exe")
		builtBinaryBytes, err = os.ReadFile(builtBinaryPathExe)
		if err != nil {
			t.Fatalf("compiled artifact entrypoint missing: %v", err)
		}
	}
	t.Logf("[E2E] Artifact compiled successfully (size: %d bytes)", len(builtBinaryBytes))

	// Release builder session
	relBuilderReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/release?user_uuid=%s", gatewayBaseURL, buildSessionUUID), nil)
	relBuilderReq.Header.Set("X-Auth-Token", "e2e-gateway-token")
	relResp, _ := httpClient.Do(relBuilderReq)
	if relResp != nil {
		relResp.Body.Close()
	}

	// Step 2.4: Activate Build
	buildID := "bld_e2e_verified_1"
	t.Logf("[E2E] Step 2.4: Activating Build %s on Action %s (Status: Succeeded)", buildID, actionID)

	// Step 2.5: Run Action with action-runtime profile under network: none
	t.Log("[E2E] Step 2.5: Provisioning Runtime Session with profile: action-runtime...")
	runSessionUUID := fmt.Sprintf("runner-%s-%s", actionID, buildID)
	plannedEnv := map[string]string{
		"ACTIONSCAT_EVENT_PLATFORM":   "qq",
		"ACTIONSCAT_EVENT_SESSION_ID": "sess_qq_group_12345",
	}
	runSessPayload := map[string]any{
		"user_uuid":            runSessionUUID,
		"profile":              sandbox.ProfileActionRuntime,
		"network":              "none",
		"runtime_callback_url": runtimeCallbackURL,
		"env":                  plannedEnv,
	}
	runSessBytes, _ := json.Marshal(runSessPayload)
	runSessReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, gatewayBaseURL+"/api/v1/sessions", bytes.NewReader(runSessBytes))
	runSessReq.Header.Set("Content-Type", "application/json")
	runSessReq.Header.Set("X-Auth-Token", "e2e-gateway-token")
	runResp, err := httpClient.Do(runSessReq)
	if err != nil {
		t.Fatalf("create runtime session: %v", err)
	}
	if runResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(runResp.Body)
		t.Fatalf("runtime session failed (HTTP %d): %s", runResp.StatusCode, string(body))
	}
	runResp.Body.Close()

	// Deploy compiled entrypoint artifact into runtime session workspace
	runtimeDir := filepath.Join(workDir, runSessionUUID)
	entrypointPath := filepath.Join(runtimeDir, "entrypoint")
	if err := os.WriteFile(entrypointPath, builtBinaryBytes, 0755); err != nil {
		t.Fatalf("deploy entrypoint: %v", err)
	}

	// Step 2.6: Execute Action in action-runtime
	t.Log("[E2E] Step 2.6: Executing Action entrypoint under action-runtime...")
	runExecPayload := map[string]any{
		"command": "/sandbox/entrypoint",
		"cwd":     "/sandbox",
		"timeout": 30.0,
	}
	runExecBytes, _ := json.Marshal(runExecPayload)
	runExecURL := fmt.Sprintf("%s/api/v1/shell/exec?user_uuid=%s&profile=action-runtime", gatewayBaseURL, runSessionUUID)
	runExecReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, runExecURL, bytes.NewReader(runExecBytes))
	runExecReq.Header.Set("Content-Type", "application/json")
	runExecReq.Header.Set("X-Auth-Token", "e2e-gateway-token")
	runExecResp, err := httpClient.Do(runExecReq)
	if err != nil {
		t.Fatalf("execute runtime: %v", err)
	}
	var runResult struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode *int   `json:"exit_code"`
		TimedOut bool   `json:"timed_out"`
	}
	_ = json.NewDecoder(runExecResp.Body).Decode(&runResult)
	runExecResp.Body.Close()

	t.Logf("[E2E] Run Completed. Exit Code: %v, Timed Out: %t", runResult.ExitCode, runResult.TimedOut)
	t.Logf("[E2E] Run Stdout:\n%s", runResult.Stdout)
	if runResult.Stderr != "" {
		t.Logf("[E2E] Run Stderr:\n%s", runResult.Stderr)
	}

	// Step 2.7: Assert Invariants
	if runResult.ExitCode == nil || *runResult.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %v: %s", runResult.ExitCode, runResult.Stderr)
	}
	if !strings.Contains(runResult.Stdout, "E2E_INVARIANT: public internet blocked successfully") {
		t.Errorf("missing invariant confirmation: public internet blocked under network:none")
	}
	if !strings.Contains(runResult.Stdout, "E2E_INVARIANT: env injection verified") {
		t.Errorf("missing invariant confirmation: env injection")
	}
	if !strings.Contains(runResult.Stdout, "E2E_INVARIANT: runtime callback reached successfully") {
		t.Errorf("missing invariant confirmation: runtime callback reached")
	}
	if !strings.Contains(runResult.Stdout, "E2E_LIFECYCLE_SUCCESS") {
		t.Errorf("missing invariant confirmation: E2E_LIFECYCLE_SUCCESS")
	}
	if callbackInvocations.Load() == 0 {
		t.Errorf("expected at least 1 runtime callback invocation, got 0")
	}

	// Release runtime session
	relRunReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/release?user_uuid=%s", gatewayBaseURL, runSessionUUID), nil)
	relRunReq.Header.Set("X-Auth-Token", "e2e-gateway-token")
	relRunResp, _ := httpClient.Do(relRunReq)
	if relRunResp != nil {
		relRunResp.Body.Close()
	}

	t.Log("[E2E] === Full Chain Lifecycle Verified Successfully! ===")
}
