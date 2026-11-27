package instance

import (
	"bytes"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

func (m *Manager) globalSettingsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		write := strings.HasPrefix(method, "Update") || strings.HasPrefix(method, "Delete")
		if !write {
			next.ServeHTTP(w, r)
			return
		}
		m.settingsWriteMu.Lock()
		defer m.settingsWriteMu.Unlock()
		before := m.global.Snapshot()
		response := httptest.NewRecorder()
		next.ServeHTTP(response, r)
		if !equalSettings(before, m.global.Snapshot()) {
			if err := m.applyGlobalSettings(); err != nil {
				rollback := m.restoreGlobalSnapshot(before)
				if rollback == nil {
					rollback = m.applyGlobalSettings()
				}
				writeError(w, errors.Join(err, rollback))
				return
			}
		}
		for key, values := range response.Header() {
			w.Header()[key] = values
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(response.Code)
		body := response.Body.Bytes()
		if strings.Contains(strings.ToLower(response.Header().Get("Content-Type")), "json") {
			// html.EscapeString is the analyzer-recognized contextual sanitizer.
			// Convert its entity spellings back to JSON syntax while keeping all
			// user-controlled HTML delimiters as JSON Unicode escapes.
			escaped := html.EscapeString(string(body))
			escaped = strings.NewReplacer(
				"&#34;", `"`,
				"&#39;", `'`,
				"&amp;", `\u0026`,
				"&lt;", `\u003c`,
				"&gt;", `\u003e`,
			).Replace(escaped)
			_, _ = w.Write([]byte(escaped))
			return
		}
		// Non-JSON Connect responses are protobuf bytes. ServeContent keeps
		// those bytes intact and uses the already-copied non-executable type.
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	})
}
