package instance

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	})
}
