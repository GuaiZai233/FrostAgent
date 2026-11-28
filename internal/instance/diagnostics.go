package instance

import (
	"context"
	"errors"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"net/http"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

type diagnostic struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Evidence any    `json:"evidence,omitempty"`
}

func (m *Manager) storageStatus(ctx context.Context) map[string]any {
	result := map[string]any{"driver": string(m.db.Backend), "connected": false, "supported_schema_version": storage.SupportedSchemaVersion}
	if err := m.db.SQL.PingContext(ctx); err != nil {
		result["error"] = "database unavailable"
		return result
	}
	result["connected"] = true
	var version, minimum int
	if err := m.db.SQL.QueryRowContext(ctx, "SELECT version, min_compatible_version FROM schema_meta WHERE id=1").Scan(&version, &minimum); err != nil {
		result["error"] = "database schema metadata unavailable"
		return result
	}
	result["schema_version"] = version
	result["minimum_compatible_version"] = minimum
	result["compatible"] = minimum <= storage.SupportedSchemaVersion
	return result
}
func (m *Manager) serveDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/api/storage/status" {
		writeJSON(w, m.storageStatus(r.Context()))
		return
	}
	checks := r.URL.Query()["check"]
	if len(checks) == 0 {
		checks = []string{"management", "storage", "instance", "onebot", "model-router", "dependencies"}
	}
	id := r.URL.Query().Get("instance")
	for _, name := range checks {
		switch name {
		case "management", "storage":
		case "instance", "onebot", "model-router", "dependencies":
			if id == "" {
				http.Error(w, "instance is required", 400)
				return
			}
		default:
			http.Error(w, "unknown diagnostic check", 400)
			return
		}
	}
	results := []diagnostic{}
	for _, name := range checks {
		d := diagnostic{Name: name, Status: "ok", Reason: "observed state is available"}
		switch name {
		case "management":
			d.Reason = "management listener authenticated this request"
		case "storage":
			d.Evidence = m.storageStatus(r.Context())
			state := d.Evidence.(map[string]any)
			if state["error"] != nil || state["compatible"] != true {
				d.Status = "failed"
				d.Reason = "storage unavailable or incompatible"
			}
		default:
			d = m.instanceDiagnostic(id, name)
		}
		results = append(results, d)
	}
	writeJSON(w, map[string]any{"checks": results})
}
func (m *Manager) instanceDiagnostic(id, name string) diagnostic {
	d := diagnostic{Name: name, Status: "ok", Reason: "observed configuration"}
	i, err := m.lookup(id)
	if err != nil {
		d.Status = "unavailable"
		d.Reason = "instance does not exist"
		return d
	}
	if !i.op.TryRLock() {
		d.Status = "unavailable"
		d.Reason = "instance lifecycle operation in progress"
		return d
	}
	defer i.op.RUnlock()
	infos, _ := m.List()
	var info Info
	for _, v := range infos {
		if v.ID == id {
			info = v
		}
	}
	i.mu.RLock()
	rt, cfg := i.runtime, i.config
	i.mu.RUnlock()
	if name == "instance" {
		d.Evidence = info
		if info.Error != "" {
			d.Status = "failed"
			d.Reason = info.Error
		} else if !info.Enabled {
			d.Status = "disabled"
			d.Reason = "instance is disabled"
		} else if rt == nil || rt.Scope.Context().Err() != nil {
			d.Status = "failed"
			d.Reason = "instance runtime is not running"
		}
		return d
	}
	if rt == nil || cfg == nil {
		d.Status = "unavailable"
		d.Reason = "instance runtime configuration unavailable"
		return d
	}
	get := rt.Scope.Getenv
	switch name {
	case "onebot":
		enabled := cfg.Get("ONEBOT_WS_ENABLED") != "false"
		count := 0
		if rt.Onebot != nil {
			count = rt.Onebot.RealConnectionCount()
		}
		lastError := ""
		for _, entry := range i.logger.Snapshot() {
			if entry.Category == logs.WEBSOCKET && (entry.Level == logs.WARN || entry.Level == logs.ERROR) {
				lastError = entry.Content
			}
		}
		d.Evidence = map[string]any{"name": "onebot", "enabled": enabled, "connections": count, "last_observed_error": lastError}
		if !enabled {
			d.Status = "disabled"
			d.Reason = "OneBot is disabled"
		} else if count == 0 {
			d.Status = "failed"
			d.Reason = "no real OneBot connection"
		}
	case "model-router":
		router := rt.Engine.ModelRouter
		active, err := router.ActiveChecked()
		if err != nil {
			d.Status = "unavailable"
			d.Reason = "model configuration unavailable"
			return d
		}
		routes := []map[string]any{}
		for _, workload := range modelrouter.Workloads {
			target, err := router.Snapshot().Resolve(workload, modelrouter.Scope{})
			route := map[string]any{"name": string(workload), "status": "ok"}
			if errors.Is(err, modelrouter.ErrDisabled) {
				route["status"] = "disabled"
			} else if err != nil {
				route["status"] = "failed"
				route["reason"] = err.Error()
				d.Status = "failed"
			} else {
				route["model_id"] = target.ModelID
				route["endpoint_id"] = target.EndpointID
				for _, endpoint := range active.Endpoints {
					if endpoint.ID == target.EndpointID {
						route["credential_configured"] = endpoint.APIKeyConfigured
						if !endpoint.APIKeyConfigured {
							route["status"] = "failed"
							d.Status = "failed"
						}
					}
				}
			}
			routes = append(routes, route)
		}
		d.Evidence = routes
	case "dependencies":
		items := []map[string]any{}
		sandboxCfg := sandbox.LoadConfig(get)
		billingCfg := billing.LoadConfig(get)
		actions := actionscat.New(get)
		for _, dep := range []struct {
			name, enabled string
			required      []string
		}{
			{"sandbox", "SANDBOX_ENABLED", []string{"SANDBOX_BASE_URL", "SANDBOX_AUTH_TOKEN"}},
			{"billing", "BILLING_ENABLED", []string{"ALCYONE_BASE_URL", "ALCYONE_SERVICE_TOKEN"}},
			{"actionscat", "ACTIONSCAT_ENABLED", []string{"ACTIONSCAT_ENDPOINT", "ACTIONSCAT_MANAGEMENT_TOKEN"}},
		} {
			enabled := get(dep.enabled) == "true"
			if dep.name == "sandbox" {
				enabled = sandboxCfg.Enabled
			}
			if dep.name == "billing" {
				enabled = billingCfg.Enabled
			}
			if dep.name == "actionscat" {
				enabled = actions.IsEnabled()
			}
			missing := []string{}
			if enabled {
				for _, key := range dep.required {
					value := get(key)
					switch key {
					case "SANDBOX_BASE_URL":
						value = sandboxCfg.BaseURL
					case "SANDBOX_AUTH_TOKEN":
						value = sandboxCfg.AuthToken
					case "ALCYONE_BASE_URL":
						value = billingCfg.BaseURL
					case "ALCYONE_SERVICE_TOKEN":
						value = billingCfg.ServiceToken
					}
					if strings.TrimSpace(value) == "" {
						missing = append(missing, key)
					}
				}
			}
			items = append(items, map[string]any{"name": dep.name, "enabled": enabled, "missing": missing, "connectivity": "not-probed"})
			if len(missing) > 0 {
				d.Status = "failed"
			}
		}
		if i.mcp != nil {
			for _, server := range i.mcp.ListServers() {
				state := server.Config()
				items = append(items, map[string]any{"name": "mcp", "id": state.ID, "enabled": state.Enabled, "connectivity": "observed-by-manager"})
			}
		}
		d.Evidence = items
	}
	return d
}
