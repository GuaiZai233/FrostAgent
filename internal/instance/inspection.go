package instance

import (
	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (m *Manager) serveBillingStatus(w http.ResponseWriter, r *http.Request) {
	shared := billing.LoadConfig(m.global.Get)
	states := []map[string]any{}
	infos, _ := m.List()
	for _, info := range infos {
		i, err := m.lookup(info.ID)
		if err != nil {
			continue
		}
		i.mu.RLock()
		enabled := false
		if i.config != nil {
			enabled = billing.LoadConfig(i.config.Get).Enabled
		}
		i.mu.RUnlock()
		states = append(states, map[string]any{"instance_id": info.ID, "enabled": enabled, "running": info.Enabled, "connection": "unobserved: billing client has no retained connection health"})
	}
	writeJSON(w, map[string]any{"credential_configured": shared.ServiceToken != "", "endpoint_configured": shared.BaseURL != "", "timeout": shared.Timeout.String(), "instances": states})
}
func (m *Manager) serveInspection(w http.ResponseWriter, r *http.Request, id, kind string) {
	i, err := m.lookup(id)
	if err != nil {
		writeError(w, err)
		return
	}
	if !i.op.TryRLock() {
		writeError(w, ErrBusy)
		return
	}
	defer i.op.RUnlock()
	if kind == "routes" {
		router := modelrouter.NewSQL(m.db, id)
		cfg, err := router.ActiveChecked()
		if err != nil {
			writeError(w, err)
			return
		}
		selected := strings.ReplaceAll(r.URL.Query().Get("workload"), "-", "_")
		routes := []map[string]any{}
		for _, item := range router.Effective(modelrouter.Scope{}) {
			if selected != "" && selected != string(item.Workload) {
				continue
			}
			route := map[string]any{"name": item.Workload, "binding": item.Binding, "inherited": item.Inherited, "runtime_applied": item.RuntimeApplied, "status": "unavailable"}
			if target, err := router.Resolve(item.Workload, modelrouter.Scope{}); err != nil {
				route["reason"] = err.Error()
			} else {
				route["status"] = "available"
				route["model_id"] = target.ModelID
				route["endpoint_id"] = target.EndpointID
				route["upstream_model"] = target.UpstreamModel
			}
			routes = append(routes, route)
		}
		if selected != "" && len(routes) == 0 {
			http.Error(w, "unknown workload", 400)
			return
		}
		writeJSON(w, map[string]any{"revision": cfg.Revision, "routes": routes, "group_overrides": cfg.GroupOverrides})
		return
	}
	trace, errorID := r.URL.Query().Get("trace"), r.URL.Query().Get("error")
	if (trace == "") == (errorID == "") {
		http.Error(w, "select exactly one trace or error", 400)
		return
	}
	entries := i.logger.Snapshot()
	if errorID != "" {
		for _, entry := range entries {
			if entry.ErrorUUID == errorID {
				trace = entry.TraceID
				break
			}
		}
	}
	type event struct {
		At       time.Time `json:"at"`
		Stage    string    `json:"stage"`
		Evidence any       `json:"evidence"`
	}
	timeline := []event{}
	available := map[string]bool{}
	for _, entry := range entries {
		if trace != "" && entry.TraceID != trace || trace == "" && entry.ErrorUUID != errorID {
			continue
		}
		stage := "other"
		switch entry.Category {
		case logs.LLM_REQUEST:
			stage = "llm-request"
		case logs.LLM_RESPONSE:
			stage = "llm-response"
		case logs.TOOL:
			stage = "tool"
		case logs.WEBSOCKET:
			if entry.Direction == "INBOUND" {
				stage = "inbound"
			} else {
				stage = "platform-transport"
			}
		}
		available[stage] = true
		// Trace metadata is useful without pulling complete prompts or payloads.
		timeline = append(timeline, event{entry.Timestamp, stage, map[string]any{"log_id": entry.ID, "error_uuid": entry.ErrorUUID, "level": entry.Level, "category": entry.Category, "direction": entry.Direction, "result": traceResult(entry), "duration": "unavailable: no duration recorded"}})
	}
	audit, err := m.security.Audit.List(0)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, item := range audit {
		if trace != "" && item.Instance == id && item.Session == trace {
			available["security"] = true
			timeline = append(timeline, event{item.At, "security", map[string]any{"id": item.ID, "stage": item.Stage, "action": item.Action, "reason": item.Reason}})
		}
	}
	sort.SliceStable(timeline, func(a, b int) bool { return timeline[a].At.Before(timeline[b].At) })
	stages := []map[string]string{}
	for _, stage := range []string{"inbound", "security", "routing", "llm-request", "llm-response", "tool", "send", "platform-receipt"} {
		state := "unavailable"
		if available[stage] {
			state = "observed"
		}
		stages = append(stages, map[string]string{"name": stage, "status": state})
	}
	writeJSON(w, map[string]any{"trace_id": trace, "error_uuid": errorID, "association": "TraceID (may identify a session, not an individual processing turn)", "timeline": timeline, "stages": stages, "coverage": i.logger.PersistenceStatus()})
}

func traceResult(entry logs.LogEntry) string {
	if entry.Level == logs.ERROR {
		return "error recorded"
	}
	if entry.Category == logs.LLM_RESPONSE {
		return "response recorded"
	}
	return "event recorded; outcome unavailable"
}
