package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// MockTransport implements Transport for testing.
type MockTransport struct {
	mu           sync.Mutex
	roundTripFn  func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error)
	notifyFn     func(ctx context.Context, notif *JSONRPCNotification) error
	closeFn      func() error
	notifications []*JSONRPCNotification
}

func (m *MockTransport) RoundTrip(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	m.mu.Lock()
	fn := m.roundTripFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil, errors.New("roundTrip not implemented")
}

func (m *MockTransport) Notify(ctx context.Context, notif *JSONRPCNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = append(m.notifications, notif)
	if m.notifyFn != nil {
		return m.notifyFn(ctx, notif)
	}
	return nil
}

func (m *MockTransport) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func TestSchemaNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    json.RawMessage
		wantType string
	}{
		{
			name:     "nil input",
			input:    nil,
			wantType: "object",
		},
		{
			name:     "empty input",
			input:    json.RawMessage(""),
			wantType: "object",
		},
		{
			name:     "no type specified",
			input:    json.RawMessage(`{"properties": {"q": {"type": "string"}}}`),
			wantType: "object",
		},
		{
			name:     "standard valid schema",
			input:    json.RawMessage(`{"type": "object", "properties": {"repo": {"type": "string"}}, "required": ["repo"]}`),
			wantType: "object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeSchema(tt.input)
			if got["type"] != tt.wantType {
				t.Fatalf("expected type %q, got %q", tt.wantType, got["type"])
			}
			if _, ok := got["properties"].(map[string]any); !ok {
				t.Fatalf("expected properties map, got %v", got["properties"])
			}
		})
	}
}

func TestResultFormatting(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		res := FormatToolResult(nil)
		if !strings.Contains(res, "successfully") {
			t.Fatalf("unexpected nil result: %s", res)
		}
	})

	t.Run("text blocks", func(t *testing.T) {
		res := FormatToolResult(&ToolCallResult{
			Content: []ContentBlock{
				{Type: "text", Text: "line 1"},
				{Type: "text", Text: "line 2"},
			},
		})
		if res != "line 1\nline 2" {
			t.Fatalf("unexpected text result: %s", res)
		}
	})

	t.Run("error block", func(t *testing.T) {
		res := FormatToolResult(&ToolCallResult{
			Content: []ContentBlock{
				{Type: "text", Text: "permission denied"},
			},
			IsError: true,
		})
		if !strings.HasPrefix(res, "MCP tool error:") || !strings.Contains(res, "permission denied") {
			t.Fatalf("unexpected error format: %s", res)
		}
	})

	t.Run("multimodal image block fallback", func(t *testing.T) {
		res := FormatToolResult(&ToolCallResult{
			Content: []ContentBlock{
				{Type: "image", MimeType: "image/png"},
			},
		})
		if !strings.Contains(res, "[image content: image/png]") {
			t.Fatalf("unexpected image format: %s", res)
		}
	})
}

func TestConfigStoreAtomicWrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mcp_cfg_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "sub", "mcp_servers.json")
	store := NewConfigStore(cfgPath)

	// Initial load should be empty
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("initial load failed: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("expected 0 servers, got %d", len(cfg.Servers))
	}

	// Save servers
	testCfg := &Config{
		Servers: []ServerConfig{
			{
				ID:      "github",
				Name:    "GitHub",
				Enabled: true,
				Transport: TransportConfig{
					Type:    TransportStdio,
					Command: "node",
					Args:    []string{"server.js"},
				},
				Tools: map[string]ToolPolicy{
					"create_issue": {Enabled: false},
				},
			},
		},
	}

	if err := store.Save(testCfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Reload and verify
	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if len(reloaded.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(reloaded.Servers))
	}
	if reloaded.Servers[0].ID != "github" {
		t.Fatalf("expected github, got %s", reloaded.Servers[0].ID)
	}
	if reloaded.Servers[0].Tools["create_issue"].Enabled {
		t.Fatalf("expected create_issue to be disabled")
	}
}

func TestToolCatalog(t *testing.T) {
	cat := NewToolCatalog()

	remoteTools := []MCPToolDefinition{
		{Name: "get_issue", Description: "Get an issue", InputSchema: json.RawMessage(`{}`)},
		{Name: "create_issue", Description: "Create an issue", InputSchema: json.RawMessage(`{}`)},
		{Name: "search_code", Description: "Search code", InputSchema: json.RawMessage(`{}`)},
	}

	policies := map[string]ToolPolicy{
		"create_issue": {Enabled: false},
	}

	cat.UpdateRemote(remoteTools, policies)

	// Check listing
	items := cat.List()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Check effective items (create_issue is disabled)
	effective := cat.EffectiveItems()
	if len(effective) != 2 {
		t.Fatalf("expected 2 effective items, got %d", len(effective))
	}
	for _, item := range effective {
		if item.RemoteName == "create_issue" {
			t.Fatalf("disabled tool create_issue should not be effective")
		}
	}

	// Dynamically enable create_issue
	cat.SetPolicy("create_issue", true)
	if len(cat.EffectiveItems()) != 3 {
		t.Fatalf("expected 3 effective items after enabling create_issue")
	}
}

func TestServerRuntimeLifecycleAndDoubleCheck(t *testing.T) {
	mockTransport := &MockTransport{
		roundTripFn: func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
			switch req.Method {
			case "initialize":
				res, _ := json.Marshal(InitializeResult{
					ProtocolVersion: ProtocolVersion,
					ServerInfo:      ServerInfo{Name: "mock-server", Version: "1.0"},
				})
				return &JSONRPCResponse{ID: req.ID, Result: res}, nil
			case "tools/list":
				res, _ := json.Marshal(ToolListResult{
					Tools: []MCPToolDefinition{
						{Name: "get_issue", Description: "fetch issue", InputSchema: json.RawMessage(`{}`)},
						{Name: "delete_repo", Description: "danger", InputSchema: json.RawMessage(`{}`)},
					},
				})
				return &JSONRPCResponse{ID: req.ID, Result: res}, nil
			case "tools/call":
				var params ToolCallParams
				_ = json.Unmarshal(req.Params, &params)
				res, _ := json.Marshal(ToolCallResult{
					Content: []ContentBlock{
						{Type: "text", Text: "issue #42 details"},
					},
				})
				return &JSONRPCResponse{ID: req.ID, Result: res}, nil
			default:
				return nil, fmt.Errorf("unexpected method: %s", req.Method)
			}
		},
	}

	serverCfg := ServerConfig{
		ID:      "github",
		Name:    "GitHub MCP",
		Enabled: true,
		Transport: TransportConfig{
			Type: TransportStdio,
		},
		Tools: map[string]ToolPolicy{
			"delete_repo": {Enabled: false},
		},
	}

	srv := NewServerRuntimeWithFactory(serverCfg, func(cfg TransportConfig) (Transport, error) {
		return mockTransport, nil
	})

	ctx := context.Background()

	// 1. Start server
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if srv.Status() != StatusConnected {
		t.Fatalf("expected status connected, got %s", srv.Status())
	}

	// 2. Call enabled tool
	res, err := srv.CallTool(ctx, "get_issue", "{}")
	if err != nil {
		t.Fatalf("call tool failed: %v", err)
	}
	if res != "issue #42 details" {
		t.Fatalf("unexpected result: %s", res)
	}

	// 3. Call disabled tool -> should return graceful error
	resDisabled, err := srv.CallTool(ctx, "delete_repo", "{}")
	if err != nil {
		t.Fatalf("call tool returned error: %v", err)
	}
	if !strings.Contains(resDisabled, "Tool \"delete_repo\" is currently disabled.") {
		t.Fatalf("expected graceful disabled message, got %q", resDisabled)
	}

	// 4. Disable entire server -> call get_issue -> should return server disabled message
	_ = srv.SetEnabled(false)
	resServerDisabled, err := srv.CallTool(ctx, "get_issue", "{}")
	if err != nil {
		t.Fatalf("call tool returned error: %v", err)
	}
	if !strings.Contains(resServerDisabled, "Tool \"get_issue\" is currently disabled because MCP server \"github\" has been disabled.") {
		t.Fatalf("expected graceful server disabled message, got %q", resServerDisabled)
	}
}

func TestManagerMultiServerAndNamespacing(t *testing.T) {
	createMockServer := func(id string, tools []string) *MockTransport {
		return &MockTransport{
			roundTripFn: func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
				switch req.Method {
				case "initialize":
					res, _ := json.Marshal(InitializeResult{
						ProtocolVersion: ProtocolVersion,
						ServerInfo:      ServerInfo{Name: id, Version: "1.0"},
					})
					return &JSONRPCResponse{ID: req.ID, Result: res}, nil
				case "tools/list":
					defs := make([]MCPToolDefinition, len(tools))
					for i, name := range tools {
						defs[i] = MCPToolDefinition{Name: name, Description: "tool " + name, InputSchema: json.RawMessage(`{}`)}
					}
					res, _ := json.Marshal(ToolListResult{Tools: defs})
					return &JSONRPCResponse{ID: req.ID, Result: res}, nil
				case "tools/call":
					var params ToolCallParams
					_ = json.Unmarshal(req.Params, &params)
					res, _ := json.Marshal(ToolCallResult{
						Content: []ContentBlock{
							{Type: "text", Text: fmt.Sprintf("%s called on %s", params.Name, id)},
						},
					})
					return &JSONRPCResponse{ID: req.ID, Result: res}, nil
				default:
					return nil, fmt.Errorf("unknown method: %s", req.Method)
				}
			},
		}
	}

	tmpDir, err := os.MkdirTemp("", "mcp_mgr_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	manager := NewManagerWithFactory(store, []string{"memory", "send_message"}, func(cfg TransportConfig) (Transport, error) {
		return createMockServer(cfg.Command, []string{"create_issue", "search"}),
			nil
	})

	ctx := context.Background()

	// Add GitHub server (provides create_issue, search)
	err = manager.AddServer(ctx, ServerConfig{
		ID:      "github",
		Name:    "GitHub",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "github-server",
		},
	})
	if err != nil {
		t.Fatalf("add github server failed: %v", err)
	}

	// Add GitLab server (also provides create_issue, search)
	err = manager.AddServer(ctx, ServerConfig{
		ID:      "gitlab",
		Name:    "GitLab",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "gitlab-server",
		},
	})
	if err != nil {
		t.Fatalf("add gitlab server failed: %v", err)
	}

	// Check Effective Tools namespacing
	effective := manager.EffectiveTools()
	if len(effective) != 4 {
		t.Fatalf("expected 4 effective tools, got %d", len(effective))
	}

	expectedNames := map[string]bool{
		"mcp__github__create_issue": true,
		"mcp__github__search":       true,
		"mcp__gitlab__create_issue": true,
		"mcp__gitlab__search":       true,
	}

	for _, tool := range effective {
		if !expectedNames[tool.Name] {
			t.Fatalf("unexpected tool name: %s", tool.Name)
		}
	}

	// Execute through tool adapters
	githubAdapter, ok := manager.LookupAdapter("mcp__github__create_issue")
	if !ok {
		t.Fatalf("lookup github adapter failed")
	}
	resGithub, err := githubAdapter.ExecuteContext(ctx, "{}")
	if err != nil {
		t.Fatalf("github execute failed: %v", err)
	}
	if !strings.Contains(resGithub, "create_issue called on github-server") {
		t.Fatalf("unexpected github res: %s", resGithub)
	}

	gitlabAdapter, ok := manager.LookupAdapter("mcp__gitlab__create_issue")
	if !ok {
		t.Fatalf("lookup gitlab adapter failed")
	}
	resGitlab, err := gitlabAdapter.ExecuteContext(ctx, "{}")
	if err != nil {
		t.Fatalf("gitlab execute failed: %v", err)
	}
	if !strings.Contains(resGitlab, "create_issue called on gitlab-server") {
		t.Fatalf("unexpected gitlab res: %s", resGitlab)
	}

	// Builtin conflict prevention
	err = manager.AddServer(ctx, ServerConfig{
		ID:      "memory", // conflicts with builtin
		Name:    "Malicious Memory",
		Enabled: true,
	})
	if err == nil {
		t.Fatalf("expected error when adding server with builtin ID 'memory'")
	}
}
