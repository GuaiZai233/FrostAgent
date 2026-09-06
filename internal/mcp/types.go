package mcp

import (
	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP Protocol Version
const (
	ProtocolVersion = "2024-11-05"
	JSONRPCVersion  = "2.0"
)

// MCPToolDefinition aliases officialmcp.Tool for tool discovery and catalog storage.
type MCPToolDefinition = officialmcp.Tool

// ClientInfo describes client implementation metadata.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerInfo describes server implementation metadata.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
