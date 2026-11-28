package settings

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"sort"
	"strings"
)

func configScopeAllows(scope, key string) bool {
	if scope == "" {
		return true
	}
	if scope == "global" {
		return instanceconfig.GlobalKeys[key] || instanceconfig.SharedKeys[key]
	}
	return scope == "instance" && !instanceconfig.GlobalKeys[key]
}
func (s *Service) GetConfigSchema(ctx context.Context, req *connect.Request[v1.GetConfigSchemaRequest]) (*connect.Response[v1.GetConfigSchemaResponse], error) {
	if err := s.databaseStateError(); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	fields := []*v1.ConfigField{}
	for key, meta := range knownEnvVars {
		if !configScopeAllows(req.Msg.Scope, key) || key == "DIALOGUE_PATH" || key == "BRAIN_PATH" || key == "ONEBOT_WS_PATH" {
			continue
		}
		scope := "instance"
		if instanceconfig.GlobalKeys[key] {
			scope = "global"
		} else if instanceconfig.SharedKeys[key] {
			scope = "shared"
		}
		defaultValue := s.staticDefault(key)
		if meta.IsSecret {
			defaultValue = ""
		}
		fields = append(fields, &v1.ConfigField{Key: key, Type: configType(key), Scope: scope, DefaultValue: defaultValue, DefaultHint: s.resetHint(key), Secret: meta.IsSecret, RestartRequired: instanceconfig.InstanceRestartKeys[key] || instanceconfig.ControlPlaneRestartKeys[key], Description: meta.Description, Constraints: "Validated by the server against the current scoped candidate configuration."})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	return connect.NewResponse(&v1.GetConfigSchemaResponse{Fields: fields}), nil
}
func (s *Service) configSource(key, value string, inherited bool) string {
	if inherited {
		return "global-inheritance"
	}
	if store := s.store(key); store != nil {
		if _, ok := store.Snapshot()[key]; ok {
			if s.config == nil || instanceconfig.GlobalKeys[key] {
				return "global"
			}
			return "instance-override"
		}
	}
	if strings.TrimSpace(value) == "" {
		return "default"
	}
	return "process"
}

// Empty defaults can be dependent on other fields; DefaultHint explains those
// cases. These are runtime fallbacks, not the template for a newly created bot.
func (s *Service) staticDefault(key string) string {
	switch key {
	case "LISTEN_ADDR":
		return instanceconfig.DefaultListenAddr
	case "WS_LISTEN_ADDR":
		return instanceconfig.DefaultWSListenAddr
	case "SECURITY_CONTROL_MODE":
		return "simple"
	case "ONEBOT_WS_URL":
		return ""
	case "MCP_ENFORCE_LOCAL_TOKEN":
		return "true"
	}
	hint := s.resetHint(key)
	switch hint {
	case "开启":
		return "true"
	case "关闭":
		return "false"
	}
	if hint == "" || strings.ContainsAny(hint, "（；，： ") {
		return ""
	}
	// Descriptive Chinese placeholders are not literal configuration values.
	for _, r := range hint {
		if r > 127 {
			return ""
		}
	}
	return hint
}
func configType(key string) string {
	switch key {
	case "BALANCE_QUERY_CONFIG", "DISABLED_TOOLS", "PROACTIVE_GROUP_PROBABILITIES":
		return "json"
	case "MAX_CONTEXT_MESSAGES", "QUOTE_TRACE_DEPTH", "STICKER_ACTIVITY", "POKE_COOLDOWN_SECONDS", "LOG_STORAGE_MIB", "AGENT_MAX_ITERATIONS", "MULTI_SEGMENT_REPLY_MAX_SEGMENTS", "BILLING_MAX_OUTPUT_TOKENS", "BILLING_PROMPT_PRICE_PER_MILLION", "BILLING_COMPLETION_PRICE_PER_MILLION":
		return "integer"
	case "BILLING_SAFETY_MULTIPLIER":
		return "number"
	}
	if strings.HasPrefix(key, "ENABLE_") || strings.HasSuffix(key, "_ENABLED") || key == "MCP_ENFORCE_LOCAL_TOKEN" {
		return "boolean"
	}
	if strings.HasSuffix(key, "_TIMEOUT") || key == "GROUP_COMPACT_MIN_INTERVAL" {
		return "duration"
	}
	return "string"
}
