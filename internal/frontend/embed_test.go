package frontend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStripLegacyLocalePrefix(t *testing.T) {
	tests := []struct {
		path     string
		wantPath string
		wantOk   bool
	}{
		{"/", "", false},
		{"/overview", "", false},
		{"/zh-Hans", "/", true},
		{"/zh-Hans/", "/", true},
		{"/zh-Hans/overview", "/overview", true},
		{"/zh-Hans/settings/backend", "/settings/backend", true},
		{"/en-US", "/", true},
		{"/en-US/", "/", true},
		{"/en-US/logs", "/logs", true},
		{"/zh-Hans-custom", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			gotPath, gotOk := stripLegacyLocalePrefix(tt.path)
			if gotOk != tt.wantOk || gotPath != tt.wantPath {
				t.Errorf("stripLegacyLocalePrefix(%q) = (%q, %v), want (%q, %v)",
					tt.path, gotPath, gotOk, tt.wantPath, tt.wantOk)
			}
		})
	}
}

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

	t.Run("redirect legacy zh-Hans prefix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/zh-Hans/settings", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected 301 StatusMovedPermanently, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/settings" {
			t.Errorf("expected Location /settings, got %q", loc)
		}
	})

	t.Run("redirect legacy en-US prefix with query", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/en-US/logs?tab=env", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected 301 StatusMovedPermanently, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/logs?tab=env" {
			t.Errorf("expected Location /logs?tab=env, got %q", loc)
		}
	})
}
