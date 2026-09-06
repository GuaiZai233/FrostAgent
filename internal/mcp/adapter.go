package mcp

import (
	"context"
	"fmt"
	"strings"

	"FrostAgent/internal/core"
)

// ToolAdapter adapts an MCP remote tool into a FrostAgent ToolExecutor.
type ToolAdapter struct {
	serverID    string
	remoteName  string
	fullName    string
	description string
	parameters  map[string]any
	manager     *Manager
}

func BuildFullName(serverID, remoteName string) string {
	return fmt.Sprintf("mcp__%s__%s", serverID, remoteName)
}

func ParseFullName(fullName string) (serverID, remoteName string, ok bool) {
	if !strings.HasPrefix(fullName, "mcp__") {
		return "", "", false
	}
	parts := strings.SplitN(fullName, "__", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func NewToolAdapter(serverID, remoteName, description string, params map[string]any, manager *Manager) *ToolAdapter {
	return &ToolAdapter{
		serverID:    serverID,
		remoteName:  remoteName,
		fullName:    BuildFullName(serverID, remoteName),
		description: description,
		parameters:  params,
		manager:     manager,
	}
}

func (t *ToolAdapter) Name() string {
	return t.fullName
}

func (t *ToolAdapter) ServerID() string {
	return t.serverID
}

func (t *ToolAdapter) RemoteName() string {
	return t.remoteName
}

func (t *ToolAdapter) Description() string {
	return t.description
}

func (t *ToolAdapter) Parameters() map[string]any {
	return t.parameters
}

func (t *ToolAdapter) ToCoreTool() core.Tool {
	return core.Tool{
		Name:        t.fullName,
		Description: t.description,
		Parameters:  t.parameters,
	}
}

func (t *ToolAdapter) Execute(args string) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *ToolAdapter) ExecuteContext(ctx context.Context, args string) (string, error) {
	if t.manager == nil {
		return "", fmt.Errorf("tool adapter has no manager attached for %s", t.fullName)
	}
	return t.manager.CallTool(ctx, t.serverID, t.remoteName, args)
}
