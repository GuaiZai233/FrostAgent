package instance

// These settings are read from the synchronized runtime scope at the point of
// use. They can change without stopping an instance or its open connections.
var hotInstanceSettings = map[string]bool{
	"SYSTEM_PROMPT": true, "BOT_NAME": true, "BOT_ALIASES": true,
	"ADMIN_QQ_IDS": true, "ADMIN_COMMAND_PREFIX": true,
	"ENABLE_AT_IN_GROUP_MSG": true, "GROUP_REPLY_ON_MENTION": true,
	"ENABLE_REPLY_IN_GROUP_MSG": true, "MAX_CONTEXT_MESSAGES": true,
	"ENABLE_QUOTE_TRACE": true, "QUOTE_TRACE_DEPTH": true,
	"BOT_LOOP_USER_IDS": true, "BOT_LOOP_MAX_TURNS": true, "BOT_LOOP_MODE": true,
	"GROUP_RAW_CONTEXT_MAX_CHARS": true,
	"MEMORY_EXTRACT_BATCH_MIN":    true, "MEMORY_EXTRACT_BATCH_MAX": true,
	"PROACTIVE_REPLY_PROBABILITY": true, "ENABLE_PROACTIVE_REPLY": true,
	"PROACTIVE_REPLY_GROUP_WHITELIST": true, "ENABLE_PROACTIVE_REPLY_WHITELIST": true,
	"PROACTIVE_REPLY_GROUP_PROBABILITIES": true,
	"STICKER_ACTIVITY":                    true, "STICKER_ACTIVITY_MODE": true, "ONEBOT_ACTION_TIMEOUT": true,
	"ACTIONSCAT_ENABLED": true, "ACTIONSCAT_ENDPOINT": true, "ACTIONSCAT_MANAGEMENT_TOKEN": true,
	"ACTIONSCAT_DISPATCH_TOKEN": true,
}

func onlyHotInstanceChanges(before, after map[string]string) bool {
	changed := false
	for key, old := range before {
		if after[key] != old {
			changed = true
			if !hotInstanceSettings[key] {
				return false
			}
		}
	}
	for key, current := range after {
		if before[key] != current {
			changed = true
			if !hotInstanceSettings[key] {
				return false
			}
		}
	}
	return changed
}

// The control-plane objects for these keys already read the SQL store or use
// an atomic manager. Other global keys may have constructor-time consumers.
var hotGlobalSettings = map[string]bool{
	"LISTEN_ADDR": true, "HTTP_ALLOWED_ORIGINS": true,
	"WS_ALLOWED_ORIGINS": true, "SANDBOX_ENABLED": true,
	"SANDBOX_BASE_URL": true, "SANDBOX_AUTH_TOKEN": true,
	"SANDBOX_SESSION_NAMESPACE": true, "SECURITY_CONTROL_MODE": true,
	"MCP_CONTROL_TOKEN": true, "ADMIN_TOKEN": true,
	"ALLOW_REMOTE_MCP_MANAGEMENT": true, "MCP_ENFORCE_LOCAL_TOKEN": true,
}

func needsGlobalRuntimeReload(before, after map[string]string) bool {
	for key, old := range before {
		if after[key] != old && !hotGlobalSettings[key] {
			return true
		}
	}
	for key, current := range after {
		if before[key] != current && !hotGlobalSettings[key] {
			return true
		}
	}
	return false
}
