package frontend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHasExtension(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/", false},
		{"/overview", false},
		{"/sessions", false},
		{"/settings/backend", false},
		{"/main.js", true},
		{"/styles.css", true},
		{"/favicon.ico", true},
		{"/assets/icon.png", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := hasExtension(tt.path); got != tt.want {
				t.Errorf("hasExtension(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestHandler(t *testing.T) {
	h := Handler()

	t.Run("root serves index.html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "<!doctype html>") && !strings.Contains(body, "<html") {
			t.Errorf("expected HTML body, got: %s", body)
		}
	})

	t.Run("spa route serves index.html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/overview", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "<!doctype html>") && !strings.Contains(body, "<html") {
			t.Errorf("expected HTML body, got: %s", body)
		}
	})

	t.Run("favicon.ico served directly", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for favicon.ico, got %d", rec.Code)
		}
	})
}
