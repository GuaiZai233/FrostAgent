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
