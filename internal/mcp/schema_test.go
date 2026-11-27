package mcp

import "testing"

func TestNormalizeSchemaDropsNonStandardKeywords(t *testing.T) {
	got := NormalizeSchema(map[string]any{
		"type":         "object",
		"returnDirect": false,
		"properties":   map[string]any{"orderId": map[string]any{"type": "string", "x-extra": 1}},
	})
	if _, ok := got["returnDirect"]; ok {
		t.Fatalf("returnDirect kept: %v", got)
	}
	if _, ok := got["properties"].(map[string]any)["orderId"].(map[string]any)["x-extra"]; ok {
		t.Fatalf("nested extra kept: %v", got)
	}
}
