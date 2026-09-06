package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func createTestServerFactory(name string, tools map[string]func(args string) (string, error)) func(TransportConfig) (officialmcp.Transport, error) {
	server := officialmcp.NewServer(&officialmcp.Implementation{Name: name, Version: "1.0"}, nil)
	for toolName, fn := range tools {
		tName := toolName
		tFn := fn
		server.AddTool(&officialmcp.Tool{
			Name:        tName,
			Description: "tool " + tName,
			InputSchema: map[string]any{"type": "object"},
		}, func(ctx context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
			args := string(req.Params.Arguments)
			out, err := tFn(args)
			if err != nil {
				return &officialmcp.CallToolResult{
					Content: []officialmcp.Content{&officialmcp.TextContent{Text: err.Error()}},
					IsError: true,
				}, nil
			}
			return &officialmcp.CallToolResult{
				Content: []officialmcp.Content{&officialmcp.TextContent{Text: out}},
			}, nil
		})
	}

	return func(cfg TransportConfig) (officialmcp.Transport, error) {
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		_, err := server.Connect(context.Background(), serverTransport, nil)
		if err != nil {
			return nil, err
		}
		return clientTransport, nil
	}
}

func TestSchemaNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		wantType string
	}{
		{
			name:     "nil input",
			input:    nil,
			wantType: "object",
		},
		{
			name:     "empty string input",
			input:    "",
			wantType: "object",
		},
		{
			name:     "no type specified",
			input:    map[string]any{"properties": map[string]any{"q": map[string]any{"type": "string"}}},
			wantType: "object",
		},
		{
			name: "standard valid schema",
			input: map[string]any{
				"type":       "object",
				"properties": map[string]any{"repo": map[string]any{"type": "string"}},
				"required":   []any{"repo"},
			},
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
		res := FormatToolResult(&officialmcp.CallToolResult{
			Content: []officialmcp.Content{
				&officialmcp.TextContent{Text: "line 1"},
				&officialmcp.TextContent{Text: "line 2"},
			},
		})
		if res != "line 1\nline 2" {
			t.Fatalf("unexpected text result: %s", res)
		}
	})

	t.Run("error block", func(t *testing.T) {
		res := FormatToolResult(&officialmcp.CallToolResult{
			Content: []officialmcp.Content{
				&officialmcp.TextContent{Text: "permission denied"},
			},
			IsError: true,
		})
		if !strings.HasPrefix(res, "MCP tool error:") || !strings.Contains(res, "permission denied") {
			t.Fatalf("unexpected error format: %s", res)
		}
	})

	t.Run("multimodal image block fallback", func(t *testing.T) {
		res := FormatToolResult(&officialmcp.CallToolResult{
			Content: []officialmcp.Content{
				&officialmcp.ImageContent{MIMEType: "image/png"},
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

	remoteTools := []*officialmcp.Tool{
		{Name: "get_issue", Description: "Get an issue", InputSchema: map[string]any{}},
		{Name: "create_issue", Description: "Create an issue", InputSchema: map[string]any{}},
		{Name: "search_code", Description: "Search code", InputSchema: map[string]any{}},
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
	tools := map[string]func(args string) (string, error){
		"get_issue": func(args string) (string, error) {
			return "issue #42 details", nil
		},
		"delete_repo": func(args string) (string, error) {
			return "repo deleted", nil
		},
	}

	factory := createTestServerFactory("mock-server", tools)

	serverCfg := ServerConfig{
		ID:      "github",
		Name:    "GitHub MCP",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "github-server",
		},
		Tools: map[string]ToolPolicy{
			"delete_repo": {Enabled: false},
		},
	}

	srv := NewServerRuntimeWithFactory(serverCfg, factory)
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
	createMockFactory := func(id string, toolNames []string) func(TransportConfig) (officialmcp.Transport, error) {
		tools := make(map[string]func(args string) (string, error))
		for _, name := range toolNames {
			n := name
			tools[n] = func(args string) (string, error) {
				return fmt.Sprintf("%s called on %s", n, id), nil
			}
		}
		return createTestServerFactory(id, tools)
	}

	tmpDir, err := os.MkdirTemp("", "mcp_mgr_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	manager := NewManagerWithFactory(store, []string{"memory", "send_message"}, func(cfg TransportConfig) (officialmcp.Transport, error) {
		return createMockFactory(cfg.Command, []string{"create_issue", "search"})(cfg)
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
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "mem-server",
		},
	})
	if err == nil {
		t.Fatalf("expected error when adding server with builtin ID 'memory'")
	}
}

func TestSanitizeExposedToolNameAndBidirectionalMapping(t *testing.T) {
	// 1. Normal valid name
	n1 := SanitizeExposedToolName("github", "create_issue")
	if n1 != "mcp__github__create_issue" {
		t.Fatalf("expected standard name, got: %s", n1)
	}

	// 2. Special characters
	n2 := SanitizeExposedToolName("git-lab.v2", "search/code@repo")
	if len(n2) > 64 || !isValidLLMName(n2) {
		t.Fatalf("expected valid LLM name <= 64 chars, got: %s (len: %d)", n2, len(n2))
	}
	if !strings.HasPrefix(n2, "mcp__git_lab_v2__") {
		t.Fatalf("expected cleaned prefix, got: %s", n2)
	}

	// 3. Very long name (> 64 chars)
	longServer := "super_extremely_long_server_organization_name_that_exceeds_limits"
	longTool := "deeply_nested_operation_with_an_incredibly_long_action_identifier_name"
	n3 := SanitizeExposedToolName(longServer, longTool)
	if len(n3) > 64 {
		t.Fatalf("expected <= 64 chars, got len: %d (%s)", len(n3), n3)
	}
	if !isValidLLMName(n3) {
		t.Fatalf("expected valid LLM name characters, got: %s", n3)
	}

	// 4. Collision resistance with hash suffix
	nA := SanitizeExposedToolName("my-server", "tool-a")
	nB := SanitizeExposedToolName("my_server", "tool_a")
	// Since "my-server" and "my_server" clean differently or contain invalid chars,
	// they must not silently collide!
	if nA == nB {
		t.Fatalf("expected distinct names for distinct inputs, got both: %s", nA)
	}

	// 5. Bidirectional mapping in manager
	tmpDir, err := os.MkdirTemp("", "mcp_sanitize_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	factory := createTestServerFactory("special-server", map[string]func(args string) (string, error){
		"tool:special": func(args string) (string, error) {
			return "special result", nil
		},
	})
	mgr := NewManagerWithFactory(store, nil, factory)

	ctx := context.Background()
	err = mgr.AddServer(ctx, ServerConfig{
		ID:      "special-server",
		Name:    "Special",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "special",
		},
	})
	if err != nil {
		t.Fatalf("add server failed: %v", err)
	}

	tools := mgr.EffectiveTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 effective tool, got %d", len(tools))
	}

	exposedName := tools[0].Name
	if !isValidLLMName(exposedName) || len(exposedName) > 64 {
		t.Fatalf("invalid exposed name: %s", exposedName)
	}

	// Lookup adapter using exposed name
	adapter, ok := mgr.LookupAdapter(exposedName)
	if !ok {
		t.Fatalf("failed to lookup adapter by exposed name: %s", exposedName)
	}
	if adapter.ServerID() != "special-server" || adapter.RemoteName() != "tool:special" {
		t.Fatalf("unexpected adapter target: %s / %s", adapter.ServerID(), adapter.RemoteName())
	}

	out, err := adapter.ExecuteContext(ctx, "{}")
	if err != nil || out != "special result" {
		t.Fatalf("execute failed: err=%v, out=%s", err, out)
	}
}

func TestServerRuntimeGenerationTokenPreventsResurrection(t *testing.T) {
	connectBlocker := make(chan struct{})
	factory := func(cfg TransportConfig) (officialmcp.Transport, error) {
		<-connectBlocker
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		server := officialmcp.NewServer(&officialmcp.Implementation{Name: "slow-server", Version: "1.0"}, nil)
		_, _ = server.Connect(context.Background(), serverTransport, nil)
		return clientTransport, nil
	}

	srv := NewServerRuntimeWithFactory(ServerConfig{
		ID:      "slow",
		Name:    "Slow Server",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "slow",
		},
	}, factory)

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- srv.Start(context.Background())
	}()

	// Wait briefly to ensure start has incremented generation and is waiting in factory
	time.Sleep(20 * time.Millisecond)

	// Stop server while connect is still in-flight
	_ = srv.Stop()

	// Unblock factory
	close(connectBlocker)
	_ = <-startErrCh

	// Server MUST NOT be connected! It was stopped and generation token mismatch must discard the session
	if srv.Status() == StatusConnected {
		t.Fatalf("slow server resurrected after Stop()!")
	}
}

func TestProcessTerminationWatcher(t *testing.T) {
	server := officialmcp.NewServer(&officialmcp.Implementation{Name: "crash-server", Version: "1.0"}, nil)
	server.AddTool(&officialmcp.Tool{
		Name:        "crash_tool",
		Description: "Crash tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
		return &officialmcp.CallToolResult{
			Content: []officialmcp.Content{&officialmcp.TextContent{Text: "ok"}},
		}, nil
	})

	var serverSession *officialmcp.ServerSession
	factory := func(cfg TransportConfig) (officialmcp.Transport, error) {
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		var err error
		serverSession, err = server.Connect(context.Background(), serverTransport, nil)
		if err != nil {
			return nil, err
		}
		return clientTransport, nil
	}

	mgr := NewManagerWithFactory(nil, nil, factory)
	ctx := context.Background()
	err := mgr.AddServer(ctx, ServerConfig{
		ID:      "crash-srv",
		Name:    "Crash Server",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "crash",
		},
	})
	if err != nil {
		t.Fatalf("add server failed: %v", err)
	}

	// Effective tools should initially have crash_tool
	tools := mgr.EffectiveTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Simulate unexpected termination of the server process by closing serverSession
	if serverSession != nil {
		_ = serverSession.Close()
	}

	// Wait for background termination watcher goroutine to detect exit
	srv, _ := mgr.GetServer("crash-srv")
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Status() == StatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if srv.Status() != StatusFailed {
		t.Fatalf("expected StatusFailed after crash, got: %s", srv.Status())
	}
	if srv.LastError() == "" {
		t.Fatalf("expected LastError to be populated after crash")
	}

	// Tool must be removed from EffectiveTools
	toolsAfterCrash := mgr.EffectiveTools()
	if len(toolsAfterCrash) != 0 {
		t.Fatalf("dead server tools still present in EffectiveTools: %v", toolsAfterCrash)
	}
}

func TestConcurrentCatalogSyncAndPolicyUpdate(t *testing.T) {
	tools := map[string]func(args string) (string, error){
		"tool1": func(args string) (string, error) { return "1", nil },
		"tool2": func(args string) (string, error) { return "2", nil },
		"tool3": func(args string) (string, error) { return "3", nil },
	}
	factory := createTestServerFactory("concurrent-server", tools)
	srv := NewServerRuntimeWithFactory(ServerConfig{
		ID:      "concurrent",
		Name:    "Concurrent",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "concurrent",
		},
		Tools: map[string]ToolPolicy{
			"tool1": {Enabled: true},
			"tool2": {Enabled: false},
		},
	}, factory)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(2)
		go func(iter int) {
			defer wg.Done()
			_ = srv.SyncCatalog(ctx)
		}(i)
		go func(iter int) {
			defer wg.Done()
			srv.SetToolEnabled("tool1", iter%2 == 0)
			srv.SetToolEnabled("tool2", iter%2 != 0)
		}(i)
	}
	wg.Wait()

	// Ensure catalog is consistent
	items := srv.Catalog().List()
	if len(items) != 3 {
		t.Fatalf("expected 3 tools in catalog, got %d", len(items))
	}
}
