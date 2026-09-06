package mcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"FrostAgent/internal/core"
)

// ToolTarget identifies a remote tool by its server ID and remote name.
type ToolTarget struct {
	ServerID   string
	RemoteName string
}

// ToolAdapter adapts an MCP remote tool into a FrostAgent ToolExecutor.
type ToolAdapter struct {
	serverID    string
	remoteName  string
	fullName    string
	description string
	parameters  map[string]any
	manager     *Manager
}

// BuildFullName returns the human-readable canonical full name.
func BuildFullName(serverID, remoteName string) string {
	return SanitizeExposedToolName(serverID, remoteName)
}

// SanitizeExposedToolName converts (serverID, remoteName) into a sanitized LLM function name.
// It ensures that the result matches ^[a-zA-Z0-9_]{1,64}$ across all LLM providers.
// If the raw "mcp__<serverID>__<remoteName>" fits within 64 chars and contains only [a-zA-Z0-9_],
// it is returned directly for human readability.
// Otherwise, characters outside [a-zA-Z0-9_] are replaced with '_', lengths are bounded,
// and an 8-character sha256 hash suffix is attached to guarantee uniqueness and prevent collisions.
func SanitizeExposedToolName(serverID, remoteName string) string {
	raw := fmt.Sprintf("mcp__%s__%s", serverID, remoteName)
	if isValidLLMName(raw) && len(raw) <= 64 {
		return raw
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(serverID+"\x00"+remoteName)))[:8]

	cleanServer := cleanNameComponent(serverID)
	cleanRemote := cleanNameComponent(remoteName)

	if cleanServer == "" {
		cleanServer = "srv"
	}
	if cleanRemote == "" {
		cleanRemote = "tool"
	}

	// Format: mcp__<server>__<remote>_<hash>
	// Max total: 64 chars.
	// Fixed: "mcp__" (5) + "__" (2) + "_" (1) + hash (8) = 16 chars.
	// Available for server + remote: 64 - 16 = 48 chars.
	maxServerLen := 20
	if len(cleanServer) > maxServerLen {
		cleanServer = cleanServer[:maxServerLen]
	}
	maxRemoteLen := 48 - len(cleanServer)
	if len(cleanRemote) > maxRemoteLen {
		cleanRemote = cleanRemote[:maxRemoteLen]
	}

	res := fmt.Sprintf("mcp__%s__%s_%s", cleanServer, cleanRemote, hash)
	if len(res) > 64 {
		res = res[:64]
	}
	return res
}

func isValidLLMName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func cleanNameComponent(s string) string {
	var sb strings.Builder
	lastUnderscore := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			sb.WriteByte(c)
			lastUnderscore = false
		} else {
			if !lastUnderscore && sb.Len() > 0 {
				sb.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(sb.String(), "_")
}

// ParseFullName parses an unsanitized or legacy full name if possible.
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
	return NewToolAdapterWithFullName(serverID, remoteName, SanitizeExposedToolName(serverID, remoteName), description, params, manager)
}

func NewToolAdapterWithFullName(serverID, remoteName, fullName, description string, params map[string]any, manager *Manager) *ToolAdapter {
	if fullName == "" {
		fullName = SanitizeExposedToolName(serverID, remoteName)
	}
	return &ToolAdapter{
		serverID:    serverID,
		remoteName:  remoteName,
		fullName:    fullName,
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
