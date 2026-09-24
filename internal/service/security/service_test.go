package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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

func TestSecurityControlModeEndpoints(t *testing.T) {
	os.Unsetenv("MCP_CONTROL_TOKEN")
	os.Unsetenv("ADMIN_TOKEN")
	os.Unsetenv("MCP_ENFORCE_LOCAL_TOKEN")
	os.Unsetenv("ALLOW_REMOTE_MCP_MANAGEMENT")

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	envContent := []byte("MCP_CONTROL_TOKEN=mode-secret-token\nMCP_ENFORCE_LOCAL_TOKEN=true\n")
	if err := os.WriteFile(envPath, envContent, 0600); err != nil {
		t.Fatal(err)
	}

	globalStore, err := instanceconfig.Open(envPath, true)
	if err != nil {
		t.Fatal(err)
	}

	ctrl := securityctl.NewController(tmpDir)
	svc := NewWithStore(ctrl, globalStore.Get, globalStore)

	// 1. GET /api/security/mode unauthorized
	req := httptest.NewRequest(http.MethodGet, "/api/security/mode", nil)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}

	// 2. GET /api/security/mode authorized -> default is simple
	req = httptest.NewRequest(http.MethodGet, "/api/security/mode", nil)
	req.Header.Set("Authorization", "Bearer mode-secret-token")
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var modeResp struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &modeResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if modeResp.Mode != string(securityctl.ControlModeSimple) {
		t.Fatalf("expected initial mode %q, got %q", securityctl.ControlModeSimple, modeResp.Mode)
	}

	// 3. POST /api/security/mode unauthorized
	req = httptest.NewRequest(http.MethodPost, "/api/security/mode", bytes.NewReader([]byte(`{"mode":"aggressive"}`)))
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for post without auth, got %d", w.Code)
	}

	// 4. POST /api/security/mode with invalid mode -> 400 Bad Request
	invalidModes := []string{`{"mode":""}`, `{"mode":"invalid_mode"}`, `{"mode":123}`, `bad json`}
	for _, body := range invalidModes {
		req = httptest.NewRequest(http.MethodPost, "/api/security/mode", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer mode-secret-token")
		w = httptest.NewRecorder()
		svc.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid body %q, got %d", body, w.Code)
		}
	}

	// 5. POST /api/security/mode with "aggressive" -> 200 OK, persists to store & updates ctrl
	req = httptest.NewRequest(http.MethodPost, "/api/security/mode", bytes.NewReader([]byte(`{"mode":"aggressive"}`)))
	req.Header.Set("Authorization", "Bearer mode-secret-token")
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &modeResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if modeResp.Mode != string(securityctl.ControlModeAggressive) {
		t.Fatalf("expected response mode %q, got %q", securityctl.ControlModeAggressive, modeResp.Mode)
	}
	if ctrl.Mode() != securityctl.ControlModeAggressive {
		t.Fatalf("expected controller mode to be aggressive, got %q", ctrl.Mode())
	}
	if globalStore.Get("SECURITY_CONTROL_MODE") != "aggressive" {
		t.Fatalf("expected store to have aggressive, got %q", globalStore.Get("SECURITY_CONTROL_MODE"))
	}

	// Verify persistence in .env file directly
	rawEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rawEnv, []byte("SECURITY_CONTROL_MODE=aggressive")) {
		t.Fatalf(".env file does not contain updated mode: %s", string(rawEnv))
	}

	// 6. POST /api/security/mode with "off" (also test trailing slash)
	req = httptest.NewRequest(http.MethodPost, "/api/security/mode/", bytes.NewReader([]byte(`{"mode":"off"}`)))
	req.Header.Set("Authorization", "Bearer mode-secret-token")
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for trailing slash POST, got %d", w.Code)
	}
	if ctrl.Mode() != securityctl.ControlModeOff {
		t.Fatalf("expected controller mode to be off, got %q", ctrl.Mode())
	}

	// 7. GET /api/security/mode (with trailing slash) -> returns "off"
	req = httptest.NewRequest(http.MethodGet, "/api/security/mode/", nil)
	req.Header.Set("Authorization", "Bearer mode-secret-token")
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for trailing slash GET, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &modeResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if modeResp.Mode != string(securityctl.ControlModeOff) {
		t.Fatalf("expected mode %q, got %q", securityctl.ControlModeOff, modeResp.Mode)
	}
}

func TestSecurityControlModeEnvironmentOverride(t *testing.T) {
	os.Unsetenv("MCP_CONTROL_TOKEN")
	os.Unsetenv("ADMIN_TOKEN")
	os.Unsetenv("MCP_ENFORCE_LOCAL_TOKEN")
	os.Unsetenv("ALLOW_REMOTE_MCP_MANAGEMENT")

	// Set process environment override for SECURITY_CONTROL_MODE
	t.Setenv("SECURITY_CONTROL_MODE", "aggressive")

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	envContent := []byte("MCP_CONTROL_TOKEN=override-token\nSECURITY_CONTROL_MODE=simple\n")
	if err := os.WriteFile(envPath, envContent, 0600); err != nil {
		t.Fatal(err)
	}

	globalStore, err := instanceconfig.Open(envPath, true)
	if err != nil {
		t.Fatal(err)
	}

	if !globalStore.HasOverride("SECURITY_CONTROL_MODE") {
		t.Fatal("expected globalStore to have override for SECURITY_CONTROL_MODE")
	}

	ctrl := securityctl.NewController(tmpDir)
	ctrl.SetMode(securityctl.ControlModeAggressive)
	svc := NewWithStore(ctrl, globalStore.Get, globalStore)

	// Attempt to update mode via POST -> should return 409 Conflict
	req := httptest.NewRequest(http.MethodPost, "/api/security/mode", bytes.NewReader([]byte(`{"mode":"off"}`)))
	req.Header.Set("Authorization", "Bearer override-token")
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on environment override, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Verify controller and store remained unchanged
	if ctrl.Mode() != securityctl.ControlModeAggressive {
		t.Fatalf("expected controller mode to remain aggressive, got %q", ctrl.Mode())
	}
	if globalStore.Get("SECURITY_CONTROL_MODE") != "aggressive" {
		t.Fatalf("expected store to remain aggressive, got %q", globalStore.Get("SECURITY_CONTROL_MODE"))
	}
}

func TestSecurityControlModeConcurrentUpdates(t *testing.T) {
	os.Unsetenv("MCP_CONTROL_TOKEN")
	os.Unsetenv("ADMIN_TOKEN")
	os.Unsetenv("MCP_ENFORCE_LOCAL_TOKEN")
	os.Unsetenv("ALLOW_REMOTE_MCP_MANAGEMENT")
	os.Unsetenv("SECURITY_CONTROL_MODE")

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	envContent := []byte("MCP_CONTROL_TOKEN=concurrent-token\n")
	if err := os.WriteFile(envPath, envContent, 0600); err != nil {
		t.Fatal(err)
	}

	globalStore, err := instanceconfig.Open(envPath, true)
	if err != nil {
		t.Fatal(err)
	}

	ctrl := securityctl.NewController(tmpDir)
	svc := NewWithStore(ctrl, globalStore.Get, globalStore)

	modes := []string{"off", "simple", "aggressive"}
	const workers = 30
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		targetMode := modes[i%len(modes)]
		go func(m string) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/security/mode", bytes.NewReader([]byte(`{"mode":"`+m+`"}`)))
			req.Header.Set("Authorization", "Bearer concurrent-token")
			w := httptest.NewRecorder()
			svc.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("concurrent update failed: status=%d body=%s", w.Code, w.Body.String())
			}
		}(targetMode)
	}

	wg.Wait()

	// Ensure final state is consistent between store and controller
	storeMode := globalStore.Get("SECURITY_CONTROL_MODE")
	ctrlMode := string(ctrl.Mode())
	if storeMode != ctrlMode {
		t.Fatalf("inconsistent final state: store=%q, ctrl=%q", storeMode, ctrlMode)
	}
}
