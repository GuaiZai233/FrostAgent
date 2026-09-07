package mcp

import (
	"encoding/json"
)

// NormalizeSchema normalizes an MCP inputSchema into an OpenAI-compatible function parameter schema.
// If input is nil or invalid, it returns a safe default {"type": "object", "properties": {}}.
func NormalizeSchema(input any) map[string]any {
	fallback := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}

	if input == nil {
		return fallback
	}

	var schema map[string]any
	switch v := input.(type) {
	case map[string]any:
		schema = v
	case []byte:
		if len(v) == 0 {
			return fallback
		}
		if err := json.Unmarshal(v, &schema); err != nil {
			return fallback
		}
	case string:
		if v == "" {
			return fallback
		}
		if err := json.Unmarshal([]byte(v), &schema); err != nil {
			return fallback
		}
	case json.RawMessage:
		if len(v) == 0 {
			return fallback
		}
		if err := json.Unmarshal(v, &schema); err != nil {
			return fallback
		}
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fallback
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			return fallback
		}
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
