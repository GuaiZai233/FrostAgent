package mcp

import (
	"encoding/json"
)

// NormalizeSchema normalizes an MCP inputSchema into an OpenAI-compatible function parameter schema.
// If input is nil or invalid, it returns a safe default {"type": "object", "properties": {}}.
func NormalizeSchema(raw json.RawMessage) map[string]any {
	fallback := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}

	if len(raw) == 0 {
		return fallback
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fallback
	}

	if schema == nil {
		return fallback
	}

	// Ensure "type" is "object"
	if t, ok := schema["type"].(string); !ok || t != "object" {
		schema["type"] = "object"
	}

	// Ensure "properties" is a map
	if props, ok := schema["properties"].(map[string]any); !ok || props == nil {
		schema["properties"] = map[string]any{}
	}

	return schema
}
