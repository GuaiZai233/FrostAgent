package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatToolResult converts an MCP ToolCallResult into a string result for FrostAgent Agent Loop.
func FormatToolResult(res *ToolCallResult) string {
	if res == nil {
		return "Tool executed successfully (no output)."
	}

	var parts []string
	for _, block := range res.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "image":
			mime := block.MimeType
			if mime == "" {
				mime = "image/unknown"
			}
			parts = append(parts, fmt.Sprintf("[image content: %s]", mime))
		case "resource":
			if block.Resource != nil {
				data, err := json.Marshal(block.Resource)
				if err == nil {
					parts = append(parts, string(data))
					continue
				}
			}
			parts = append(parts, "[resource content]")
		default:
			if block.Text != "" {
				parts = append(parts, block.Text)
			} else if block.Data != "" {
				parts = append(parts, fmt.Sprintf("[%s content: %s]", block.Type, block.MimeType))
			} else {
				parts = append(parts, fmt.Sprintf("[%s content]", block.Type))
			}
		}
	}

	out := strings.Join(parts, "\n")
	if out == "" {
		if res.IsError {
			return "MCP tool execution failed (no error details returned)."
		}
		return "Tool executed successfully (no output)."
	}

	if res.IsError {
		return "MCP tool error: " + out
	}
	return out
}
