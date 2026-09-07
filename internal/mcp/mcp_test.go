package mcp

import (
	"context"
	"errors"
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
		{Name: "create_issue", Description: "Create issue", InputSchema: map[string]any{}},
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
	_ = srv.SetEnabled(ctx, false)
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

	// AddServer now guarantees durable config, not completed startup. Wait until the
	// test servers are connected before asserting their discovered tool catalogs.
	for _, id := range []string{"github", "gitlab"} {
		srv, ok := manager.GetServer(id)
		if !ok {
			t.Fatalf("expected server %s to be registered", id)
		}
		waitForStatus(t, srv, StatusConnected)
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

	srv, ok := mgr.GetServer("special-server")
	if !ok {
		t.Fatal("expected special-server to be registered")
	}
	waitForStatus(t, srv, StatusConnected)

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

	// 6. Disable the tool: EffectiveTools must omit it, but LookupAdapter must still resolve and return graceful disabled notice!
	err = mgr.SetToolEnabled("special-server", "tool:special", false)
	if err != nil {
		t.Fatalf("SetToolEnabled failed: %v", err)
	}
	toolsAfterDisable := mgr.EffectiveTools()
	if len(toolsAfterDisable) != 0 {
		t.Fatalf("expected 0 effective tools after disabling, got %d", len(toolsAfterDisable))
	}

	adapterAfterDisable, ok := mgr.LookupAdapter(exposedName)
	if !ok {
		t.Fatalf("expected LookupAdapter to resolve disabled tool by its stable exposed name: %s", exposedName)
	}
	outDisabled, err := adapterAfterDisable.ExecuteContext(ctx, "{}")
	if err != nil {
		t.Fatalf("unexpected execution error for disabled tool: %v", err)
	}
	if !strings.Contains(outDisabled, "currently disabled") {
		t.Fatalf("expected graceful disabled message for sanitized tool, got: %q", outDisabled)
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

type testClosableTransport struct {
	officialmcp.Transport
	onConnect func(officialmcp.Connection)
}

func (t *testClosableTransport) Connect(ctx context.Context) (officialmcp.Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if t.onConnect != nil {
		t.onConnect(conn)
	}
	return conn, nil
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

	var activeConn officialmcp.Connection
	factory := func(cfg TransportConfig) (officialmcp.Transport, error) {
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		_, err := server.Connect(context.Background(), serverTransport, nil)
		if err != nil {
			return nil, err
		}
		return &testClosableTransport{
			Transport: clientTransport,
			onConnect: func(c officialmcp.Connection) {
				activeConn = c
			},
		}, nil
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

	srv, ok := mgr.GetServer("crash-srv")
	if !ok {
		t.Fatal("expected crash-srv to be registered")
	}
	waitForStatus(t, srv, StatusConnected)

	// Effective tools should initially have crash_tool
	tools := mgr.EffectiveTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Simulate unexpected termination of the server process by abruptly closing connection
	if activeConn != nil {
		_ = activeConn.Close()
	}

	// Wait for background termination watcher goroutine to detect exit
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

func TestTransportHTTPConfiguration(t *testing.T) {
	// SSE transport
	sseTransport, err := CreateTransport(TransportConfig{
		Type:    TransportSSE,
		URL:     "http://localhost:8080/sse",
		Headers: map[string]string{"Authorization": "Bearer token123"},
	})
	if err != nil {
		t.Fatalf("failed to create SSE transport: %v", err)
	}
	sseClientTransport, ok := sseTransport.(*officialmcp.SSEClientTransport)
	if !ok {
		t.Fatalf("expected SSEClientTransport, got %T", sseTransport)
	}
	if sseClientTransport.HTTPClient.Timeout != 0 {
		t.Fatalf("expected Timeout 0 for streaming SSE, got %v", sseClientTransport.HTTPClient.Timeout)
	}

	// Streamable HTTP transport
	streamTransport, err := CreateTransport(TransportConfig{
		Type: TransportStreamableHTTP,
		URL:  "http://localhost:8080/stream",
	})
	if err != nil {
		t.Fatalf("failed to create StreamableHTTP transport: %v", err)
	}
	streamClientTransport, ok := streamTransport.(*officialmcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("expected StreamableClientTransport, got %T", streamTransport)
	}
	if streamClientTransport.HTTPClient.Timeout != 0 {
		t.Fatalf("expected Timeout 0 for StreamableHTTP, got %v", streamClientTransport.HTTPClient.Timeout)
	}
}

func TestServerRuntimeLifecycleDecoupledFromStartupContext(t *testing.T) {
	tools := map[string]func(args string) (string, error){
		"ping": func(args string) (string, error) { return "pong", nil },
	}
	factory := createTestServerFactory("decoupled-server", tools)
	srv := NewServerRuntimeWithFactory(ServerConfig{
		ID:      "decoupled",
		Name:    "Decoupled Server",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "decoupled",
		},
	}, factory)

	// Call Start with a short-lived context that gets cancelled immediately after Start returns
	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := srv.Start(startCtx); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	cancelStart() // Cancel caller's context

	// Server must still be connected because session lifecycle is decoupled!
	if srv.Status() != StatusConnected {
		t.Fatalf("server disconnected when startup ctx was cancelled, status=%s", srv.Status())
	}

	// Tools must still be callable
	res, err := srv.CallTool(context.Background(), "ping", "{}")
	if err != nil || res != "pong" {
		t.Fatalf("tool call failed after startup ctx cancellation: res=%q, err=%v", res, err)
	}

	_ = srv.Stop()
}

func TestServerRuntimeSetEnabledIdempotency(t *testing.T) {
	connectCount := 0
	factory := func(cfg TransportConfig) (officialmcp.Transport, error) {
		connectCount++
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		server := officialmcp.NewServer(&officialmcp.Implementation{Name: "idem", Version: "1.0"}, nil)
		server.AddTool(&officialmcp.Tool{
			Name:        "t",
			InputSchema: map[string]any{"type": "object"},
		}, func(ctx context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
			return &officialmcp.CallToolResult{
				Content: []officialmcp.Content{&officialmcp.TextContent{Text: "ok"}},
			}, nil
		})
		_, _ = server.Connect(context.Background(), serverTransport, nil)
		return clientTransport, nil
	}

	srv := NewServerRuntimeWithFactory(ServerConfig{
		ID:      "idem",
		Name:    "Idem Server",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "idem",
		},
	}, factory)

	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if connectCount != 1 {
		t.Fatalf("expected 1 connect, got %d", connectCount)
	}

	ctx := context.Background()

	// Call SetEnabled(true) repeatedly on connected server -> must be a no-op!
	if err := srv.SetEnabled(ctx, true); err != nil {
		t.Fatalf("SetEnabled(true) failed: %v", err)
	}
	if err := srv.SetEnabled(ctx, true); err != nil {
		t.Fatalf("SetEnabled(true) failed: %v", err)
	}
	if connectCount != 1 {
		t.Fatalf("expected connectCount to remain 1 after redundant SetEnabled(true), got %d", connectCount)
	}

	// Calling Stop then SetEnabled(false) -> must be a no-op!
	_ = srv.Stop()
	if err := srv.SetEnabled(ctx, false); err != nil {
		t.Fatalf("SetEnabled(false) failed: %v", err)
	}
	if srv.Status() != StatusStopped {
		t.Fatalf("expected StatusStopped, got %s", srv.Status())
	}
}

func TestServerRuntimeToolListChangedNotification(t *testing.T) {
	server := officialmcp.NewServer(&officialmcp.Implementation{Name: "dyn", Version: "1.0"}, &officialmcp.ServerOptions{
		Capabilities: &officialmcp.ServerCapabilities{
			Tools: &officialmcp.ToolCapabilities{ListChanged: true},
		},
	})
	server.AddTool(&officialmcp.Tool{
		Name:        "tool_v1",
		Description: "Version 1",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
		return &officialmcp.CallToolResult{Content: []officialmcp.Content{&officialmcp.TextContent{Text: "v1"}}}, nil
	})

	factory := func(cfg TransportConfig) (officialmcp.Transport, error) {
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		_, err := server.Connect(context.Background(), serverTransport, nil)
		if err != nil {
			return nil, err
		}
		return clientTransport, nil
	}

	srv := NewServerRuntimeWithFactory(ServerConfig{
		ID:      "dyn",
		Name:    "Dynamic Server",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "dyn",
		},
	}, factory)

	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer srv.Stop()

	if len(srv.Catalog().List()) != 1 {
		t.Fatalf("expected 1 tool initially, got %d", len(srv.Catalog().List()))
	}

	// Now dynamically add tool_v2 to server. Since ListChanged is true, server automatically broadcasts notification
	server.AddTool(&officialmcp.Tool{
		Name:        "tool_v2",
		Description: "Version 2",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
		return &officialmcp.CallToolResult{Content: []officialmcp.Content{&officialmcp.TextContent{Text: "v2"}}}, nil
	})

	// Client's ToolListChangedHandler should automatically trigger SyncCatalog
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.Catalog().List()) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(srv.Catalog().List()) != 2 {
		t.Fatalf("expected 2 tools after dynamic list_changed notification, got %d", len(srv.Catalog().List()))
	}
	if _, ok := srv.Catalog().Get("tool_v2"); !ok {
		t.Fatalf("expected tool_v2 in catalog after sync")
	}
}

func TestManager_SetServerEnabled_PropagatesStartupErrorAndContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mcp_mgr_err_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	manager := NewManagerWithFactory(store, []string{"memory"}, func(cfg TransportConfig) (officialmcp.Transport, error) {
		if cfg.Command == "nonexistent-cmd" {
			return nil, errors.New("simulated transport connection failure")
		}
		return nil, errors.New("unknown server")
	})

	ctx := context.Background()
	// Add server initially disabled
	err = manager.AddServer(ctx, ServerConfig{
		ID:      "failing_server",
		Name:    "Failing Server",
		Enabled: false,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "nonexistent-cmd",
		},
	})
	if err != nil {
		t.Fatalf("AddServer failed: %v", err)
	}

	// 1. Enabling failing server must propagate the startup error rather than false success!
	enableErr := manager.SetServerEnabled(ctx, "failing_server", true)
	if enableErr == nil {
		t.Fatalf("expected SetServerEnabled to return error, got nil")
	}
	if !strings.Contains(enableErr.Error(), "simulated transport connection failure") {
		t.Fatalf("expected error to contain simulated failure, got: %v", enableErr)
	}

	// Verify server status reflects failure
	srv, ok := manager.GetServer("failing_server")
	if !ok {
		t.Fatalf("server not found")
	}
	if srv.Status() != StatusFailed {
		t.Fatalf("expected server status to be StatusFailed, got %s", srv.Status())
	}

	// 2. Test context cancellation
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	cancelErr := manager.SetServerEnabled(canceledCtx, "failing_server", true)
	if cancelErr == nil {
		t.Fatalf("expected error on canceled context, got nil")
	}
}
