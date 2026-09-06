package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// Client handles the MCP protocol layer over a Transport.
type Client struct {
	transport Transport
	reqID     atomic.Int64
}

func NewClient(transport Transport) *Client {
	return &Client{
		transport: transport,
	}
}

func (c *Client) nextID() int64 {
	return c.reqID.Add(1)
}

// Initialize performs the MCP handshake:
// 1. send "initialize" request
// 2. receive InitializeResult
// 3. send "notifications/initialized" notification
func (c *Client) Initialize(ctx context.Context, clientInfo ClientInfo) (*InitializeResult, error) {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      clientInfo,
	}

	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal initialize params: %w", err)
	}

	req := &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      c.nextID(),
		Method:  "initialize",
		Params:  paramsBytes,
	}

	resp, err := c.transport.RoundTrip(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("initialize roundtrip failed: %w", err)
	}

	if resp.Error != nil {
		return nil, resp.Error
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal initialize result: %w", err)
	}

	// Send notifications/initialized
	notif := &JSONRPCNotification{
		JSONRPC: JSONRPCVersion,
		Method:  "notifications/initialized",
	}
	if err := c.transport.Notify(ctx, notif); err != nil {
		// Non-fatal notification error, but log if needed
	}

	return &result, nil
}

// ListTools discovers all available tools from the MCP server, paging through cursors if necessary.
func (c *Client) ListTools(ctx context.Context) ([]MCPToolDefinition, error) {
	var allTools []MCPToolDefinition
	var cursor string

	for {
		paramsMap := map[string]any{}
		if cursor != "" {
			paramsMap["cursor"] = cursor
		}
		paramsBytes, _ := json.Marshal(paramsMap)

		req := &JSONRPCRequest{
			JSONRPC: JSONRPCVersion,
			ID:      c.nextID(),
			Method:  "tools/list",
			Params:  paramsBytes,
		}

		resp, err := c.transport.RoundTrip(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("tools/list failed: %w", err)
		}

		if resp.Error != nil {
			return nil, resp.Error
		}

		var listResult ToolListResult
		if err := json.Unmarshal(resp.Result, &listResult); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tools/list result: %w", err)
		}

		allTools = append(allTools, listResult.Tools...)
		if listResult.NextCursor == "" {
			break
		}
		cursor = listResult.NextCursor
	}

	return allTools, nil
}

// CallTool executes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolCallResult, error) {
	params := ToolCallParams{
		Name:      name,
		Arguments: arguments,
	}

	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool call params: %w", err)
	}

	req := &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      c.nextID(),
		Method:  "tools/call",
		Params:  paramsBytes,
	}

	resp, err := c.transport.RoundTrip(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("tools/call transport error: %w", err)
	}

	if resp.Error != nil {
		return &ToolCallResult{
			Content: []ContentBlock{
				{
					Type: "text",
					Text: fmt.Sprintf("MCP server error (%d): %s", resp.Error.Code, resp.Error.Message),
				},
			},
			IsError: true,
		}, nil
	}

	var callResult ToolCallResult
	if err := json.Unmarshal(resp.Result, &callResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools/call result: %w", err)
	}

	return &callResult, nil
}

func (c *Client) Close() error {
	if c.transport != nil {
		return c.transport.Close()
	}
	return nil
}
