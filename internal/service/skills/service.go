package skills

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	mcpsvc "FrostAgent/internal/service/mcp"
	skillstore "FrostAgent/internal/skills"
)

type Service struct {
	store  *skillstore.Store
	getenv func(string) string
}

func New(store *skillstore.Store, getenv func(string) string) *Service {
	return &Service{store: store, getenv: getenv}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := mcpsvc.CheckControlPlaneAuthScoped(r.RemoteAddr, r.Header, s.getenv); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/skills")
	var value any
	var err error
	switch {
	case path == "" || path == "/":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		value, err = s.store.List()
	case path == "/import":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, skillstore.MaxZIPBytes)
		data, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "ZIP 最大为 16 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		value, err = s.store.ImportZIP(data)
	default:
		if !strings.HasPrefix(path, "/items/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path, "/items/")
		if name == "" || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			var pkg skillstore.Package
			pkg, err = s.store.Get(name)
			value = struct {
				Info   skillstore.Info `json:"info"`
				Body   string          `json:"body"`
				Source string          `json:"source"`
			}{pkg.Info, pkg.Body, string(pkg.Files["SKILL.md"])}
		case http.MethodPost:
			var request struct {
				Enabled *bool `json:"enabled"`
			}
			decoder := json.NewDecoder(io.LimitReader(r.Body, 1024))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil || request.Enabled == nil {
				http.Error(w, "enabled 必须为布尔值", 400)
				return
			}
			err = s.store.SetEnabled(name, *request.Enabled)
			value = map[string]bool{"success": err == nil}
		case http.MethodDelete:
			err = s.store.Delete(name)
			value = map[string]bool{"success": err == nil}
		default:
			http.Error(w, "method not allowed", 405)
			return
		}
	}
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
