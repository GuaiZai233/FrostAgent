package sandbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"FrostAgent/internal/sandbox"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestCheckReadiness_EndpointUnreachable(t *testing.T) {
	ctx := context.Background()

	// Case 1: Empty endpoint
	rep := sandbox.CheckReadiness(ctx, "", "")
	if rep.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected status %s, got %s", sandbox.StatusEndpointUnreachable, rep.Status)
	}
	if rep.Healthy || rep.Authenticated || rep.ContractSupported {
		t.Fatalf("expected all false, got: %+v", rep)
	}

	// Case 2: Invalid scheme
	rep = sandbox.CheckReadiness(ctx, "ftp://invalid-scheme", "")
	if rep.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected status %s, got %s", sandbox.StatusEndpointUnreachable, rep.Status)
	}

	// Case 3: Connection refused / dead port
	rep = sandbox.CheckReadiness(ctx, "http://127.0.0.1:54321", "token")
	if rep.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected status %s, got %s", sandbox.StatusEndpointUnreachable, rep.Status)
	}
	if !strings.Contains(rep.Detail, "endpoint unreachable") {
		t.Fatalf("expected detail to contain 'endpoint unreachable', got: %s", rep.Detail)
	}
}

func TestCheckReadiness_AuthFailure(t *testing.T) {
	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token != "valid-secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"invalid authentication credentials"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	// Call with invalid token
	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "wrong-token")
	if rep.Status != sandbox.StatusAuthFailure {
		t.Fatalf("expected status %s, got %s", sandbox.StatusAuthFailure, rep.Status)
	}
	if !rep.Healthy {
		t.Fatalf("expected healthy true (server responded), got false")
	}
	if rep.Authenticated {
		t.Fatalf("expected authenticated false, got true")
	}
	if !strings.Contains(rep.Detail, "authentication failed") {
		t.Fatalf("expected detail to mention auth failed, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_APIContractMissing_Sessions404(t *testing.T) {
	ctx := context.Background()

	// Simulate Foxerine/code-interpreter where /api/v1/status exists,
	// but /api/v1/sessions returns 404!
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token != "my-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/shell/exec":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stdout":"ok","exit_code":0}`))
		default:
			// /api/v1/sessions is NOT implemented!
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "my-secret")
	if rep.Status != sandbox.StatusAPIContractMissing {
		t.Fatalf("expected status %s, got %s", sandbox.StatusAPIContractMissing, rep.Status)
	}
	if !rep.Healthy || !rep.Authenticated {
		t.Fatalf("expected healthy and authenticated to be true, got: %+v", rep)
	}
	if rep.ContractSupported {
		t.Fatalf("expected ContractSupported to be false, got true")
	}
	if !strings.Contains(rep.Detail, "404") {
		t.Fatalf("expected detail to mention 404, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_APIContractMissing_Exec404(t *testing.T) {
	ctx := context.Background()

	// Simulate gateway where /sessions exists but /shell/exec returns 404
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token != "my-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"route /api/v1/shell/exec not found"}`))
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "my-secret")
	if rep.Status != sandbox.StatusAPIContractMissing {
		t.Fatalf("expected status %s, got %s", sandbox.StatusAPIContractMissing, rep.Status)
	}
	if !strings.Contains(rep.Detail, "shell/exec") {
		t.Fatalf("expected detail to mention shell/exec, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_APIContractMissing_Release404(t *testing.T) {
	ctx := context.Background()

	// Simulate gateway where /sessions and /shell/exec exist, but /release returns 404
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token != "my-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			exitCode := 0
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    "ok",
				"exit_code": exitCode,
			})
		case "/api/v1/release":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"release endpoint not found"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "my-secret")
	if rep.Status != sandbox.StatusAPIContractMissing {
		t.Fatalf("expected status %s, got %s", sandbox.StatusAPIContractMissing, rep.Status)
	}
	if !strings.Contains(rep.Detail, "release") {
		t.Fatalf("expected detail to mention release, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_SessionConformanceFailure(t *testing.T) {
	ctx := context.Background()

	// Gateway returns empty JSON object {} for /api/v1/sessions
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "")
	if rep.Status != sandbox.StatusAPIContractMissing {
		t.Fatalf("expected status %s for empty JSON session response, got %s", sandbox.StatusAPIContractMissing, rep.Status)
	}
	if !strings.Contains(rep.Detail, "conformance") {
		t.Fatalf("expected detail to mention conformance, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_Server5xx_NotProfileUnsupported(t *testing.T) {
	ctx := context.Background()

	// Gateway returns HTTP 500 on /sessions
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"docker daemon unreachable"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "")
	if rep.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected 500 to be classified as endpoint_unreachable, got: %s (detail: %s)", rep.Status, rep.Detail)
	}
	if strings.Contains(rep.Detail, "profile_unsupported") {
		t.Fatalf("must not misclassify 500 as profile_unsupported")
	}
	if !strings.Contains(rep.Detail, "500") {
		t.Fatalf("expected detail to mention 500, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_GoToolchainMissing(t *testing.T) {
	ctx := context.Background()

	// Gateway accepts go-builder session, but 'go version' fails inside the container
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			var req struct {
				Command string `json:"command"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Command == "go version" {
				exitCode := 127
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"stderr":    "bash: go: command not found",
					"exit_code": exitCode,
				})
				return
			}
			exitCode := 0
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    "ok",
				"exit_code": exitCode,
			})
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "", sandbox.ProfileGoBuilder)
	if rep.Status != sandbox.StatusProfileUnsupported {
		t.Fatalf("expected status %s when Go toolchain is missing, got %s (detail: %s)", sandbox.StatusProfileUnsupported, rep.Status, rep.Detail)
	}
	if !strings.Contains(rep.Detail, "go-builder") || !strings.Contains(rep.Detail, "toolchain") {
		t.Fatalf("expected detail to mention go-builder toolchain missing, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_ProfileUnsupported(t *testing.T) {
	ctx := context.Background()

	// Simulate gateway that supports /api/v1/sessions, but rejects "action-runtime" with 422
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token != "my-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				Profile  string `json:"profile"`
				UserUUID string `json:"user_uuid"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)

			if body.Profile == sandbox.ProfileMinimal || body.Profile == sandbox.ProfileGoBuilder {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"user_uuid": body.UserUUID,
					"profile":   body.Profile,
					"network":   body.Network,
					"status":    "ready",
				})
				return
			}

			// action-runtime rejected!
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":"profile 'action-runtime' not supported"}`))
		case "/api/v1/shell/exec":
			exitCode := 0
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    "ok",
				"exit_code": exitCode,
			})
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "my-secret", sandbox.ProfileGoBuilder, sandbox.ProfileActionRuntime)
	if rep.Status != sandbox.StatusProfileUnsupported {
		t.Fatalf("expected status %s, got %s", sandbox.StatusProfileUnsupported, rep.Status)
	}
	if !rep.Healthy || !rep.Authenticated || !rep.ContractSupported {
		t.Fatalf("expected healthy, authenticated, contract supported, got: %+v", rep)
	}
	if !rep.ProfilesSupported[sandbox.ProfileGoBuilder] {
		t.Fatalf("expected go-builder to be true")
	}
	if rep.ProfilesSupported[sandbox.ProfileActionRuntime] {
		t.Fatalf("expected action-runtime to be false")
	}
	if !strings.Contains(rep.Detail, "action-runtime") {
		t.Fatalf("expected detail to mention rejected profile, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_FullyReady(t *testing.T) {
	ctx := context.Background()

	var releasedMu sync.Mutex
	released := make(map[string]bool)
	var observedUUIDsMu sync.Mutex
	observedUUIDs := make([]string, 0)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token != "valid-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok","supported_profiles":["go-builder","action-runtime","minimal"]}`))
		case "/api/v1/sessions":
			var body struct {
				Profile  string `json:"profile"`
				UserUUID string `json:"user_uuid"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)

			// Strict RFC4122 UUID check
			if !uuidRegex.MatchString(body.UserUUID) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"detail":"user_uuid must be a valid RFC 4122 UUID"}`))
				return
			}

			observedUUIDsMu.Lock()
			observedUUIDs = append(observedUUIDs, body.UserUUID)
			observedUUIDsMu.Unlock()

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			userUUID := r.URL.Query().Get("user_uuid")
			if !uuidRegex.MatchString(userUUID) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"detail":"user_uuid query parameter must be a valid UUID"}`))
				return
			}

			var req struct {
				Command string `json:"command"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			exitCode := 0
			stdout := "ok"
			if req.Command == "go version" {
				stdout = "go version go1.24.0 linux/amd64"
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    stdout,
				"exit_code": exitCode,
				"timed_out": false,
			})
		case "/api/v1/release":
			userUUID := r.URL.Query().Get("user_uuid")
			if !uuidRegex.MatchString(userUUID) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"detail":"user_uuid query parameter must be a valid UUID"}`))
				return
			}
			releasedMu.Lock()
			released[userUUID] = true
			releasedMu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "valid-token", sandbox.ProfileGoBuilder, sandbox.ProfileActionRuntime)
	if rep.Status != sandbox.StatusReady {
		t.Fatalf("expected status %s, got %s (detail: %s)", sandbox.StatusReady, rep.Status, rep.Detail)
	}
	if !rep.Healthy || !rep.Authenticated || !rep.ContractSupported {
		t.Fatalf("expected all true, got: %+v", rep)
	}
	if !rep.ProfilesSupported[sandbox.ProfileGoBuilder] || !rep.ProfilesSupported[sandbox.ProfileActionRuntime] {
		t.Fatalf("expected all requested profiles to be supported, got: %+v", rep.ProfilesSupported)
	}

	// Verify all observed UUIDs were unique and valid UUIDv4 format
	observedUUIDsMu.Lock()
	defer observedUUIDsMu.Unlock()
	if len(observedUUIDs) < 3 {
		t.Fatalf("expected at least 3 sessions (contract + go-builder + action-runtime), got %d", len(observedUUIDs))
	}
	uuidSet := make(map[string]bool)
	for _, u := range observedUUIDs {
		if !uuidRegex.MatchString(u) {
			t.Errorf("expected valid UUID, got %q", u)
		}
		if uuidSet[u] {
			t.Errorf("duplicate UUID detected: %q", u)
		}
		uuidSet[u] = true
	}

	// Verify all sessions were released
	releasedMu.Lock()
	defer releasedMu.Unlock()
	for _, u := range observedUUIDs {
		if !released[u] {
			t.Errorf("expected session %q to be released", u)
		}
	}
}

func TestValidateSessionResponse_ConformanceRegression(t *testing.T) {
	validUUID := "123e4567-e89b-12d3-a456-426614174000"
	validProfile := sandbox.ProfileGoBuilder

	tests := []struct {
		name            string
		body            string
		expectedUUID    string
		expectedProfile string
		expectedNetwork string
		wantErrSubstr   string
	}{
		{
			name:            "empty body",
			body:            "",
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "empty session response body",
		},
		{
			name:            "missing required fields",
			body:            `{"user_uuid":""}`,
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "missing required fields",
		},
		{
			name:            "uuid case mismatch rejected",
			body:            `{"user_uuid":"123E4567-E89B-12D3-A456-426614174000","profile":"go-builder","network":"none","status":"ready"}`,
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "user_uuid mismatch",
		},
		{
			name:            "profile mismatch rejected",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"minimal","network":"none","status":"ready"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "profile mismatch",
		},
		{
			name:            "missing network policy rejected",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"go-builder","status":"ready"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "missing network policy",
		},
		{
			name:            "wrong network policy rejected",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"go-builder","network":"public","status":"ready"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "network policy mismatch",
		},
		{
			name:            "status case mismatch rejected Ready",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"go-builder","network":"none","status":"Ready"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "session status not ready/created",
		},
		{
			name:            "status case mismatch rejected CREATED",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"go-builder","network":"none","status":"CREATED"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "session status not ready/created",
		},
		{
			name:            "invalid status rejected",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"go-builder","network":"none","status":"running"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
			wantErrSubstr:   "session status not ready/created",
		},
		{
			name:            "valid ready response passes",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"go-builder","network":"none","status":"ready"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
		},
		{
			name:            "valid created response passes",
			body:            fmt.Sprintf(`{"user_uuid":%q,"profile":"go-builder","network":"none","status":"created"}`, validUUID),
			expectedUUID:    validUUID,
			expectedProfile: validProfile,
			expectedNetwork: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := sandbox.ValidateSessionResponse([]byte(tt.body), tt.expectedUUID, tt.expectedProfile, tt.expectedNetwork)
			if tt.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSubstr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp == nil || resp.UserUUID != tt.expectedUUID || resp.Profile != tt.expectedProfile {
					t.Fatalf("unexpected response parsed: %+v", resp)
				}
			}
		})
	}
}

func TestCheckReadiness_ShellExec_TransportError(t *testing.T) {
	ctx := context.Background()

	// Case A: Transport error during contract /shell/exec
	tsContractExecFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			http.Error(w, "hijack failed", http.StatusInternalServerError)
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer tsContractExecFail.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, tsContractExecFail.Client(), tsContractExecFail.URL, "")
	if rep.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected contract exec transport error to yield %s, got %s (detail: %s)", sandbox.StatusEndpointUnreachable, rep.Status, rep.Detail)
	}
	if !strings.Contains(rep.Detail, "transport error") {
		t.Fatalf("expected detail to mention transport error, got: %s", rep.Detail)
	}

	// Case B: Transport error during profile /shell/exec
	tsProfileExecFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			var req struct {
				Command string `json:"command"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Command == "go version" {
				hj, ok := w.(http.Hijacker)
				if ok {
					conn, _, _ := hj.Hijack()
					_ = conn.Close()
					return
				}
				http.Error(w, "hijack failed", http.StatusInternalServerError)
				return
			}
			exitCode := 0
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    "ok",
				"exit_code": exitCode,
			})
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer tsProfileExecFail.Close()

	repProf := sandbox.CheckReadinessWithClient(ctx, tsProfileExecFail.Client(), tsProfileExecFail.URL, "", sandbox.ProfileGoBuilder)
	if repProf.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected profile exec transport error to yield %s, got %s (detail: %s)", sandbox.StatusEndpointUnreachable, repProf.Status, repProf.Detail)
	}
	if strings.Contains(repProf.Detail, "profile_unsupported") {
		t.Fatalf("must not misclassify transport error as profile_unsupported: %s", repProf.Detail)
	}
}

func TestCheckReadiness_ShellExec_MalformedResponse(t *testing.T) {
	ctx := context.Background()

	// Server returns HTTP 200 with malformed JSON on /shell/exec
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not-json-response`))
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "")
	if rep.Status != sandbox.StatusAPIContractMissing {
		t.Fatalf("expected malformed 200 response to yield %s, got %s (detail: %s)", sandbox.StatusAPIContractMissing, rep.Status, rep.Detail)
	}
	if !strings.Contains(rep.Detail, "conformance") {
		t.Fatalf("expected detail to mention conformance error, got: %s", rep.Detail)
	}
}

func TestCheckReadiness_ProfileReleaseFailure(t *testing.T) {
	ctx := context.Background()

	var releaseCount int
	var releaseCountMu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			exitCode := 0
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    "ok",
				"exit_code": exitCode,
			})
		case "/api/v1/release":
			releaseCountMu.Lock()
			releaseCount++
			count := releaseCount
			releaseCountMu.Unlock()

			if count == 1 {
				// Contract session release succeeds
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// Profile session release fails with 500!
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"failed to remove container"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rep := sandbox.CheckReadinessWithClient(ctx, ts.Client(), ts.URL, "", sandbox.ProfileMinimal)
	if rep.Status == sandbox.StatusReady {
		t.Fatalf("MUST NOT be ready when profile release fails! Got: %+v", rep)
	}
	if rep.Status != sandbox.StatusEndpointUnreachable {
		t.Fatalf("expected 500 on profile release to yield %s, got %s (detail: %s)", sandbox.StatusEndpointUnreachable, rep.Status, rep.Detail)
	}
	if !strings.Contains(rep.Detail, "releasing profile") {
		t.Fatalf("expected detail to mention releasing profile error, got: %s", rep.Detail)
	}
}
