package instance

import (
	"FrostAgent/internal/envinject"
	settingssvc "FrostAgent/internal/service/settings"
	"encoding/json"
	"net/http"
	"strings"
)

func (m *Manager) serveExtraEnv(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Cache-Control", "no-store")
	const base = "global/extra-env"
	if path == base {
		switch r.Method {
		case http.MethodGet:
			entries, err := envinject.List(r.Context(), m.db)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, map[string]any{"entries": entries})
		case http.MethodPut:
			var input envinject.Entry
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<10)).Decode(&input); err != nil {
				http.Error(w, "无效的环境变量内容", http.StatusBadRequest)
				return
			}
			if settingssvc.IsManagedKey(input.Key) {
				http.Error(w, "此项应在常规设置中修改", http.StatusBadRequest)
				return
			}
			if err := envinject.Validate(input.Key, input.Value); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := envinject.Put(r.Context(), m.db, input.Key, input.Value); err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, map[string]bool{"success": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if r.Method != http.MethodDelete || !strings.HasPrefix(path, base+"/") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	key := strings.TrimPrefix(path, base+"/")
	if strings.Contains(key, "/") || settingssvc.IsManagedKey(key) {
		http.Error(w, "无效的环境变量名", http.StatusBadRequest)
		return
	}
	if err := envinject.Validate(key, ""); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := envinject.Delete(r.Context(), m.db, key); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}
