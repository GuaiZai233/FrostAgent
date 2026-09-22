package sandbox_test

import (
	"context"
	"encoding/json"
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
