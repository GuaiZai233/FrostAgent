package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"FrostAgent/internal/instanceconfig"
	securityctl "FrostAgent/internal/security"
)

func TestSecurityAuthHonorsScopedConfigWithoutProcessEnv(t *testing.T) {
	// Ensure process environment does not leak tokens into this test.
	os.Unsetenv("MCP_CONTROL_TOKEN")
	os.Unsetenv("ADMIN_TOKEN")
	os.Unsetenv("MCP_ENFORCE_LOCAL_TOKEN")
	os.Unsetenv("ALLOW_REMOTE_MCP_MANAGEMENT")

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	envContent := []byte("MCP_CONTROL_TOKEN=store-secret-token-xyz\nMCP_ENFORCE_LOCAL_TOKEN=true\n")
	if err := os.WriteFile(envPath, envContent, 0600); err != nil {
		t.Fatal(err)
	}

	globalStore, err := instanceconfig.Open(envPath, true)
	if err != nil {
		t.Fatal(err)
	}

	ctrl := securityctl.NewController(tmpDir)
	svc := NewScoped(ctrl, globalStore.Get)

	// Lock a principal first
	principal, err := securityctl.NewPrincipal("qq", "synthetic-user-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Lock(principal, "test lock"); err != nil {
		t.Fatal(err)
	}

	// 1. GET /api/security/locked without Authorization header -> 401
	req := httptest.NewRequest(http.MethodGet, "/api/security/locked", nil)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth header, got %d", w.Code)
	}

	// 2. GET /api/security/locked with wrong Authorization header -> 401
	req = httptest.NewRequest(http.MethodGet, "/api/security/locked", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong auth header, got %d", w.Code)
	}

	// 3. GET /api/security/locked with valid store-configured Authorization header -> 200
	req = httptest.NewRequest(http.MethodGet, "/api/security/locked", nil)
	req.Header.Set("Authorization", "Bearer store-secret-token-xyz")
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid auth header, got %d", w.Code)
	}

	var lockedResp struct {
		Locked []securityctl.AccessRecord `json:"locked"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lockedResp); err != nil {
		t.Fatalf("unmarshal locked response: %v", err)
	}
	if len(lockedResp.Locked) != 1 || lockedResp.Locked[0].Principal.Key() != principal.Key() {
		t.Fatalf("unexpected locked response: %+v", lockedResp)
	}

	// 4. POST /api/security/unlock without Authorization header -> 401
	unlockBody := []byte(`{"platform":"qq","user_id":"synthetic-user-123"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/security/unlock", bytes.NewReader(unlockBody))
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unlock without auth header, got %d", w.Code)
	}

	// 5. POST /api/security/unlock with valid Authorization header -> 200 and unlocks principal
	req = httptest.NewRequest(http.MethodPost, "/api/security/unlock", bytes.NewReader(unlockBody))
	req.Header.Set("Authorization", "Bearer store-secret-token-xyz")
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for unlock with valid auth header, got %d", w.Code)
	}

	locked, _, err := ctrl.Access.IsLocked(principal)
	if err != nil || locked {
		t.Fatalf("expected principal to be unlocked, locked=%v err=%v", locked, err)
	}
}
