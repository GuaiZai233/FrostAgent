package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/sandbox/gateway"
)

func TestGatewayServer_LifecycleAndAuth(t *testing.T) {
	tempDir := t.TempDir()
	gw := gateway.NewServer(gateway.Config{
		AuthToken: "test-auth-secret",
		WorkDir:   tempDir,
	})
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	ctx := context.Background()

	// 1. Status without auth -> 401
	statusReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/status", nil)
	resp, err := ts.Client().Do(statusReq)
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without auth token, got: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Status with auth -> 200
	statusReq, _ = http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/status", nil)
	statusReq.Header.Set("X-Auth-Token", "test-auth-secret")
	resp, err = ts.Client().Do(statusReq)
	if err != nil {
		t.Fatalf("status request with auth: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", resp.StatusCode)
	}
	var statusData struct {
		Status            string   `json:"status"`
		SupportedProfiles []string `json:"supported_profiles"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&statusData)
	resp.Body.Close()

	if statusData.Status != "ok" {
		t.Fatalf("expected status 'ok', got: %s", statusData.Status)
	}
	hasGoBuilder := false
	for _, p := range statusData.SupportedProfiles {
		if p == "go-builder" {
			hasGoBuilder = true
		}
	}
	if !hasGoBuilder {
		t.Fatalf("expected supported_profiles to include go-builder, got: %v", statusData.SupportedProfiles)
	}

	// 3. Create Session with invalid profile -> 422
	badSessBody, _ := json.Marshal(map[string]any{
		"user_uuid": "sess-user-1",
		"profile":   "unsupported-profile-xyz",
	})
	badSessReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/sessions", bytes.NewReader(badSessBody))
	badSessReq.Header.Set("X-Auth-Token", "test-auth-secret")
	badSessReq.Header.Set("Content-Type", "application/json")
	resp, err = ts.Client().Do(badSessReq)
	if err != nil {
		t.Fatalf("create session invalid profile: %v", err)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 Unprocessable Entity for invalid profile, got: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. Create Session with go-builder -> 200
	validSessBody, _ := json.Marshal(map[string]any{
		"user_uuid":            "sess-user-1",
		"profile":              "go-builder",
		"network":              "none",
		"runtime_callback_url": "http://127.0.0.1:8080/runtime",
		"env":                  map[string]string{"ENV_A": "VAL_A"},
	})
	sessReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/sessions", bytes.NewReader(validSessBody))
	sessReq.Header.Set("X-Auth-Token", "test-auth-secret")
	sessReq.Header.Set("Content-Type", "application/json")
	resp, err = ts.Client().Do(sessReq)
	if err != nil {
		t.Fatalf("create session valid: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}
	var sessData struct {
		UserUUID string `json:"user_uuid"`
		Profile  string `json:"profile"`
		Status   string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&sessData)
	resp.Body.Close()

	if sessData.UserUUID != "sess-user-1" || sessData.Profile != "go-builder" || sessData.Status != "ready" {
		t.Fatalf("unexpected session response: %+v", sessData)
	}

	// 5. Execute Command in Session
	execBody, _ := json.Marshal(map[string]any{
		"command": "echo hello-gateway",
		"cwd":     "/sandbox",
		"timeout": 10.0,
	})
	execURL := fmt.Sprintf("%s/api/v1/shell/exec?user_uuid=sess-user-1", ts.URL)
	execReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, execURL, bytes.NewReader(execBody))
	execReq.Header.Set("X-Auth-Token", "test-auth-secret")
	execReq.Header.Set("Content-Type", "application/json")
	resp, err = ts.Client().Do(execReq)
	if err != nil {
		t.Fatalf("exec request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", resp.StatusCode)
	}
	var execData struct {
		Stdout   string `json:"stdout"`
		ExitCode *int   `json:"exit_code"`
		TimedOut bool   `json:"timed_out"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&execData)
	resp.Body.Close()

	if execData.ExitCode == nil || *execData.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got: %v", execData.ExitCode)
	}
	if !strings.Contains(execData.Stdout, "hello-gateway") {
		t.Fatalf("expected stdout to contain 'hello-gateway', got: %s", execData.Stdout)
	}

	// 6. Release Session
	relURL := fmt.Sprintf("%s/api/v1/release?user_uuid=sess-user-1", ts.URL)
	relReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, relURL, nil)
	relReq.Header.Set("X-Auth-Token", "test-auth-secret")
	resp, err = ts.Client().Do(relReq)
	if err != nil {
		t.Fatalf("release request: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 7. Release again -> 404 (idempotent)
	resp, err = ts.Client().Do(relReq)
	if err != nil {
		t.Fatalf("release again: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found on released session, got: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 8. Test Readiness Diagnostic Probe against Gateway
	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "test-auth-secret", "go-builder", "action-runtime")
	if rep.Status != sandbox.StatusReady {
		t.Fatalf("expected readiness StatusReady, got: %s (detail: %s)", rep.Status, rep.Detail)
	}
	if !rep.Healthy || !rep.Authenticated || !rep.ContractSupported {
		t.Fatalf("expected healthy, authenticated, and contract supported to be true, got: %+v", rep)
	}
}
