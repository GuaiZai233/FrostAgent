package sandbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"FrostAgent/internal/sandbox"
)

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

func TestCheckReadiness_APIContractMissing(t *testing.T) {
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

func TestCheckReadiness_ProfileUnsupported(t *testing.T) {
	ctx := context.Background()

	// Simulate gateway that supports /api/v1/sessions, but rejects "action-runtime"
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
			}
			_ = json.NewDecoder(r.Body).Decode(&body)

			if body.Profile == sandbox.ProfileMinimal || body.Profile == sandbox.ProfileGoBuilder {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"user_uuid": body.UserUUID,
					"profile":   body.Profile,
					"status":    "ready",
				})
				return
			}

			// action-runtime rejected!
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":"profile 'action-runtime' not supported"}`))
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
			}
			_ = json.NewDecoder(r.Body).Decode(&body)

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"status":    "ready",
			})
		case "/api/v1/release":
			releasedMu.Lock()
			released[r.URL.Query().Get("user_uuid")] = true
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

	// Verify probe sessions were released cleanly
	releasedMu.Lock()
	defer releasedMu.Unlock()
	if !released["readiness-probe-contract-test"] {
		t.Errorf("expected contract probe session to be released")
	}
	if !released["readiness-probe-go-builder"] {
		t.Errorf("expected go-builder probe session to be released")
	}
	if !released["readiness-probe-action-runtime"] {
		t.Errorf("expected action-runtime probe session to be released")
	}
}
