package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddlewareRejectsUnknownOrigin(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "https://admin.example.com")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := corsMiddleware(next)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "https://evil.example.com")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected disallowed origin to return 403, got %d", recorder.Code)
	}
}

func TestCORSMiddlewareAllowsConfiguredOrigin(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "https://admin.example.com,http://custom.local:3000")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := corsMiddleware(next)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "https://admin.example.com")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected allowed origin to return 204, got %d", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "https://admin.example.com" {
		t.Fatalf("expected Access-Control-Allow-Origin header to be set")
	}
}

func TestCORSMiddlewareAllowsMatchingSameOriginScheme(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Host = "127.0.0.1:8080"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected same-origin request to pass, got %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "https://127.0.0.1:8080")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected cross-scheme origin to be rejected, got %d", recorder.Code)
	}
}

func TestCORSMiddlewareRejectsDNSRebinding(t *testing.T) {
	// Attacker binds attacker.example to 127.0.0.1.
	// Request sent with Origin == Host == "attacker.example:8080" and loopback RemoteAddr.
	t.Setenv("HTTP_ALLOWED_ORIGINS", "")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api", nil)
	request.Host = "attacker.example:8080"
	request.Header.Set("Origin", "http://attacker.example:8080")
	request.RemoteAddr = "127.0.0.1:54321"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected DNS rebinding request (Origin == Host == attacker.example, RemoteAddr=127.0.0.1) to be rejected with 403, got %d", recorder.Code)
	}
}

func TestCORSMiddlewareRejectsDNSRebindingOnMCPControlPlane(t *testing.T) {
	// DNS Rebinding attack against MCP Control Plane:
	// Attacker binds attacker.example to 127.0.0.1.
	// Browser sends POST to /frostagent.v1.MCPService/AddMCPServer with:
	// Origin: http://attacker.example:8080, Host: attacker.example:8080, RemoteAddr: 127.0.0.1:12345
	t.Setenv("HTTP_ALLOWED_ORIGINS", "")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/frostagent.v1.MCPService/AddMCPServer", nil)
	request.Host = "attacker.example:8080"
	request.Header.Set("Origin", "http://attacker.example:8080")
	request.RemoteAddr = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected DNS rebinding MCP request (Origin=Host=attacker.example, RemoteAddr=127.0.0.1) to be rejected with 403, got %d", recorder.Code)
	}
}

func TestCORSMiddlewareRejectsUnallowedHostWithoutOrigin(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	request.Host = "evil.example.com:8080"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected unallowed host to be rejected with 403, got %d", recorder.Code)
	}
}

func TestCORSMiddlewareAllowsExplicitRemoteHostAndOrigin(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "https://admin.example.com")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "https://admin.example.com/api", nil)
	request.Host = "admin.example.com"
	request.Header.Set("Origin", "https://admin.example.com")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected explicit remote host and origin to pass, got %d", recorder.Code)
	}
}

func TestCORSMiddlewareOptionsPreflight(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "http://127.0.0.1:5173")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodOptions, "http://127.0.0.1:8080/frostagent.v1.BotStatusService/GetOverview", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for OPTIONS, got %d", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5173" {
		t.Fatalf("expected Access-Control-Allow-Origin header")
	}
}

func TestControlPlaneOriginsUseGlobalStoreGetter(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "https://untrusted.example")
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), func(key string) string {
		if key == "HTTP_ALLOWED_ORIGINS" {
			return "https://panel.example"
		}
		return ""
	})
	for _, host := range []string{"panel.example", "untrusted.example"} {
		req := httptest.NewRequest("OPTIONS", "https://"+host+"/api/instances", nil)
		req.Header.Set("Origin", "https://"+host)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		want := 403
		if host == "panel.example" {
			want = 204
		}
		if w.Code != want {
			t.Fatalf("%s status=%d", host, w.Code)
		}
	}
}
