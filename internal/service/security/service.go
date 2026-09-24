package security

import (
	"FrostAgent/internal/instanceconfig"
	securityctl "FrostAgent/internal/security"
	mcpsvc "FrostAgent/internal/service/mcp"
	"encoding/json"
	"net/http"
	"strings"
)

type Service struct {
	controller *securityctl.Controller
	getenv     func(string) string
	store      *instanceconfig.Store
	updater    func(key, value string) error
}

func New(controller *securityctl.Controller) *Service {
	return NewScoped(controller, nil)
}

func NewScoped(controller *securityctl.Controller, getenv func(string) string) *Service {
	return &Service{controller: controller, getenv: getenv}
}

func NewWithStore(controller *securityctl.Controller, getenv func(string) string, store *instanceconfig.Store) *Service {
	return &Service{controller: controller, getenv: getenv, store: store}
}

func NewWithUpdater(controller *securityctl.Controller, getenv func(string) string, updater func(key, value string) error) *Service {
	return &Service{controller: controller, getenv: getenv, updater: updater}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var getenv func(string) string
	if s != nil {
		getenv = s.getenv
	}
	if err := mcpsvc.CheckControlPlaneAuthScoped(remoteAddr(r), r.Header, getenv); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s == nil || s.controller == nil {
		http.Error(w, "security control unavailable", http.StatusServiceUnavailable)
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/security/locked" && r.Method == http.MethodGet:
		records, err := s.controller.Access.ListLocked()
		if err != nil {
			http.Error(w, "security state unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"locked": records})
	case path == "/api/security/unlock" && r.Method == http.MethodPost:
		var body struct {
			Platform string `json:"platform"`
			UserID   string `json:"user_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		principal, err := securityctl.NewPrincipal(body.Platform, body.UserID)
		if err != nil {
			http.Error(w, "invalid principal", http.StatusBadRequest)
			return
		}
		if err := s.controller.Unlock(principal); err != nil {
			http.Error(w, "unlock failed", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"unlocked": principal})
	case path == "/api/security/mode" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"mode": s.controller.Mode()})
	case path == "/api/security/mode" && r.Method == http.MethodPost:
		var body struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rawMode := strings.TrimSpace(body.Mode)
		if rawMode == "" || !securityctl.IsValidControlMode(rawMode) {
			http.Error(w, "invalid mode: must be off, simple, or aggressive", http.StatusBadRequest)
			return
		}
		mode := securityctl.ParseControlMode(rawMode)
		if s.store != nil {
			if err := s.store.Update("SECURITY_CONTROL_MODE", string(mode), false); err != nil {
				http.Error(w, "failed to persist mode: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else if s.updater != nil {
			if err := s.updater("SECURITY_CONTROL_MODE", string(mode)); err != nil {
				http.Error(w, "failed to persist mode: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		s.controller.SetMode(mode)
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode})
	default:
		http.NotFound(w, r)
	}
}

func remoteAddr(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
