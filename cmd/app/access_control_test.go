package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectListenerAllowsLoopbackWithoutToken(t *testing.T) {
	t.Setenv("TEST_ACCESS_TOKEN", "")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	handler, err := protectListener("127.0.0.1:8080", "TEST_ACCESS_TOKEN", "test", next)
	if err != nil {
		t.Fatalf("protectListener returned error: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected loopback request to pass, got %d", recorder.Code)
	}
}

func TestProtectListenerRequiresTokenForRemoteBind(t *testing.T) {
	t.Setenv("TEST_ACCESS_TOKEN", "")
	if _, err := protectListener("0.0.0.0:8080", "TEST_ACCESS_TOKEN", "test", http.NotFoundHandler()); err == nil {
		t.Fatal("expected remote listener without token to fail")
	}
}

func TestAccessTokenMiddlewareAcceptsBearerAndBasic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := accessTokenMiddleware("secret-token", "test", next)

	for name, authorization := range map[string]string{
		"bearer": "Bearer secret-token",
		"basic":  "Basic " + base64.StdEncoding.EncodeToString([]byte("frostagent:secret-token")),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", authorization)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("expected authorized request to pass, got %d", recorder.Code)
			}
		})
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing token to return 401, got %d", recorder.Code)
	}
}

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

func TestCORSMiddlewareAllowsOnlyMatchingSameOriginScheme(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected same-origin request to pass, got %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/", nil)
	request.Header.Set("Origin", "https://127.0.0.1:8080")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected cross-scheme origin to be rejected, got %d", recorder.Code)
	}
}
