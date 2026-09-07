package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// FormatToolResult converts an MCP CallToolResult into a string result for FrostAgent Agent Loop.
func FormatToolResult(res *officialmcp.CallToolResult) string {
	if res == nil {
		return "Tool executed successfully (no output)."
	}

	var parts []string
	for _, item := range res.Content {
		switch block := item.(type) {
		case *officialmcp.TextContent:
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case *officialmcp.ImageContent:
			mime := block.MIMEType
			if mime == "" {
				mime = "image/unknown"
			}
			parts = append(parts, fmt.Sprintf("[image content: %s]", mime))
		case *officialmcp.EmbeddedResource:
			if block.Resource != nil {
				data, err := json.Marshal(block.Resource)
				if err == nil {
					parts = append(parts, string(data))
					continue
				}
			}
			parts = append(parts, "[resource content]")
		default:
			if block != nil {
				data, err := json.Marshal(block)
				if err == nil {
					parts = append(parts, string(data))
				}
			}
		}
	}

	if res.StructuredContent != nil {
		data, err := json.Marshal(res.StructuredContent)
		if err == nil {
			parts = append(parts, string(data))
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
