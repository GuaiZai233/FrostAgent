package actionscat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	client "FrostAgent/internal/actionscat"
	mcpsvc "FrostAgent/internal/service/mcp"
)

// Service exposes an HTTP API for the FrostAgent frontend and internal caller to manage ActionsCat.
type Service struct {
	client     *client.Client
	instanceID string
	getenv     func(string) string
}

// New creates a new ActionsCat HTTP management service.
func New(c *client.Client, instanceID string) *Service {
	return NewScoped(c, instanceID, nil)
}

// NewScoped creates a new ActionsCat HTTP management service with scoped environment lookup for control-plane auth.
func NewScoped(c *client.Client, instanceID string, getenv func(string) string) *Service {
	return &Service{
		client:     c,
		instanceID: instanceID,
		getenv:     getenv,
	}
}

// Client returns the underlying ActionsCat client.
func (s *Service) Client() *client.Client {
	return s.client
}

// ServeHTTP handles requests under /api/actionscat/
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var getenv func(string) string
	if s != nil {
		getenv = s.getenv
	}
	if err := mcpsvc.CheckControlPlaneAuthScoped(remoteAddr(r), r.Header, getenv); err != nil {
		s.writeError(w, http.StatusUnauthorized, "unauthorized: "+err.Error())
		return
	}

	// Strip prefix
	path := r.URL.Path
	for _, prefix := range []string{"/api/actionscat", "/api/v1/actionscat"} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	if path == "" {
		path = "/"
	}

	switch {
	case path == "/status" || path == "/status/":
		if r.Method != http.MethodGet {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleStatus(w, r)

	case path == "/actions" || path == "/actions/":
		if r.Method == http.MethodGet {
			s.handleListActions(w, r)
			return
		}
		if r.Method == http.MethodPost {
			s.handleCreateAction(w, r)
			return
		}
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")

	case strings.HasPrefix(path, "/actions/"):
		s.handleActionsSubpath(w, r, strings.TrimPrefix(path, "/actions/"))

	case path == "/dispatch" || path == "/dispatch/":
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleDispatch(w, r)

	default:
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("actionscat path %q not found", r.URL.Path))
	}
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := s.client.Status(r.Context())
	s.writeJSON(w, http.StatusOK, st)
}

func (s *Service) handleListActions(w http.ResponseWriter, r *http.Request) {
	if !s.client.IsConfigured() {
		s.writeError(w, http.StatusBadRequest, "ActionsCat 未配置 ACTIONSCAT_ENDPOINT")
		return
	}
	actions, err := s.client.ListActions(r.Context())
	if err != nil {
		s.handleClientError(w, err)
		return
	}
	if actions == nil {
		actions = []client.Action{}
	}
	s.writeJSON(w, http.StatusOK, actions)
}

func (s *Service) handleCreateAction(w http.ResponseWriter, r *http.Request) {
	if !s.client.IsConfigured() {
		s.writeError(w, http.StatusBadRequest, "ActionsCat 未配置 ACTIONSCAT_ENDPOINT")
		return
	}
	if r.Body == nil {
		s.writeError(w, http.StatusBadRequest, "empty request body")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	var req client.CreateActionReq
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
		return
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		s.writeError(w, http.StatusBadRequest, "unexpected trailing json tokens")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		s.writeError(w, http.StatusBadRequest, "action name cannot be empty")
		return
	}
	action, err := s.client.CreateAction(r.Context(), req)
	if err != nil {
		s.handleClientError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, action)
}

func (s *Service) handleActionsSubpath(w http.ResponseWriter, r *http.Request, relPath string) {
	if !s.client.IsConfigured() {
		s.writeError(w, http.StatusBadRequest, "ActionsCat 未配置 ACTIONSCAT_ENDPOINT")
		return
	}

	relPath = strings.Trim(relPath, "/")
	parts := strings.Split(relPath, "/")

	// Unescape segments
	for i, p := range parts {
		if unescaped, err := url.PathUnescape(p); err == nil {
			parts[i] = unescaped
		}
	}

	switch len(parts) {
	case 1:
		// GET /actions/:id
		actionID := parts[0]
		if r.Method != http.MethodGet {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		act, err := s.client.GetAction(r.Context(), actionID)
		if err != nil {
			s.handleClientError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, act)

	case 2:
		actionID := parts[0]
		if parts[1] != "runs" {
			s.writeError(w, http.StatusNotFound, "unknown actions sub-resource")
			return
		}
		if r.Method == http.MethodGet {
			// GET /actions/:id/runs?limit=...&offset=...
			limit := 50
			offset := 0
			if lStr := r.URL.Query().Get("limit"); lStr != "" {
				if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
					limit = l
				}
			}
			if oStr := r.URL.Query().Get("offset"); oStr != "" {
				if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
					offset = o
				}
			}
			runs, err := s.client.ListRuns(r.Context(), actionID, limit, offset)
			if err != nil {
				s.handleClientError(w, err)
				return
			}
			if runs == nil {
				runs = []client.Run{}
			}
			s.writeJSON(w, http.StatusOK, runs)
			return
		}

		if r.Method == http.MethodPost {
			// POST /actions/:id/runs
			var req client.ManualRunReq
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
				dec := json.NewDecoder(r.Body)
				if err := dec.Decode(&req); err != nil {
					if !errors.Is(err, io.EOF) {
						s.writeError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
						return
					}
				} else {
					var trailing json.RawMessage
					if err := dec.Decode(&trailing); err != io.EOF {
						s.writeError(w, http.StatusBadRequest, "unexpected trailing json tokens")
						return
					}
				}
			}
			for k := range req.ExtraEnv {
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(k)), "ACTIONSCAT_") {
					s.writeError(w, http.StatusBadRequest, fmt.Sprintf("environment variable %q uses reserved prefix 'ACTIONSCAT_'", k))
					return
				}
			}
			run, err := s.client.TriggerRun(r.Context(), actionID, req)
			if err != nil {
				s.handleClientError(w, err)
				return
			}
			s.writeJSON(w, http.StatusCreated, run)
			return
		}
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")

	case 3:
		// GET /actions/:id/runs/:rid
		actionID := parts[0]
		if parts[1] != "runs" {
			s.writeError(w, http.StatusNotFound, "unknown actions sub-resource")
			return
		}
		runID := parts[2]
		if r.Method != http.MethodGet {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		run, err := s.client.GetRun(r.Context(), actionID, runID)
		if err != nil {
			s.handleClientError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, run)

	case 4:
		// GET /actions/:id/runs/:rid/logs
		actionID := parts[0]
		if parts[1] != "runs" || parts[3] != "logs" {
			s.writeError(w, http.StatusNotFound, "unknown actions sub-resource")
			return
		}
		runID := parts[2]
		if r.Method != http.MethodGet {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		logs, err := s.client.GetRunLogs(r.Context(), actionID, runID)
		if err != nil {
			s.handleClientError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, logs)

	default:
		s.writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Service) handleDispatch(w http.ResponseWriter, r *http.Request) {
	if !s.client.IsConfigured() {
		s.writeError(w, http.StatusBadRequest, "ActionsCat 未配置 ACTIONSCAT_ENDPOINT")
		return
	}
	if r.Body == nil {
		s.writeError(w, http.StatusBadRequest, "empty request body")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	var payload map[string]any
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
		return
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		s.writeError(w, http.StatusBadRequest, "unexpected trailing json tokens")
		return
	}

	if envRaw, ok := payload["extra_env"]; ok {
		if envMap, ok := envRaw.(map[string]any); ok {
			for k := range envMap {
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(k)), "ACTIONSCAT_") {
					s.writeError(w, http.StatusBadRequest, fmt.Sprintf("environment variable %q uses reserved prefix 'ACTIONSCAT_'", k))
					return
				}
			}
		}
	}

	if err := s.client.Dispatch(r.Context(), payload); err != nil {
		s.handleClientError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Service) handleClientError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, client.ErrNotFound):
		s.writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, client.ErrNotConfigured):
		s.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, client.ErrInvalidURL):
		s.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		s.writeError(w, http.StatusGatewayTimeout, "ActionsCat request timeout: "+err.Error())
	default:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Service) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Service) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func remoteAddr(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.RemoteAddr)
}

