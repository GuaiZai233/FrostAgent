package llm

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
)

const DisabledToolsKey = "DISABLED_TOOLS"

// ToolEnabled is the instance-level switch, independent of prerequisite checks.
func (e *Engine) ToolEnabled(name string) bool {
	if e == nil || e.Scope == nil || e.Scope.Config == nil {
		return true
	}
	raw := e.Scope.Config.Get(DisabledToolsKey)
	if raw == "" {
		return true
	}
	var disabled []string
	if json.Unmarshal([]byte(raw), &disabled) != nil {
		return false
	}
	for _, item := range disabled {
		if item == name || name == qqextend.ToolName && item == "qq" {
			return false
		}
	}
	return true
}
