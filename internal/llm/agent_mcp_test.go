package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/mcp"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func createTestMCPFactory(name string, tools map[string]func(args string) (string, error)) func(mcp.TransportConfig) (officialmcp.Transport, error) {
	server := officialmcp.NewServer(&officialmcp.Implementation{Name: name, Version: "1.0"}, nil)
	for toolName, fn := range tools {
		tName := toolName
		tFn := fn
		server.AddTool(&officialmcp.Tool{
			Name:        tName,
			Description: "Fetch " + tName,
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

	return func(cfg mcp.TransportConfig) (officialmcp.Transport, error) {
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		_, err := server.Connect(context.Background(), serverTransport, nil)
		if err != nil {
			return nil, err
		}
		return clientTransport, nil
	}
}

type scriptedLLMProvider struct {
	mu        sync.Mutex
	rounds    int
	reqTools  [][]core.Tool
	responses []*core.ChatResponse
}

func (s *scriptedLLMProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Capture tools sent in this round
	s.reqTools = append(s.reqTools, req.Tools)
	idx := s.rounds
	s.rounds++

	if idx < len(s.responses) {
		return s.responses[idx], nil
	}
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "default final response",
		},
	}, nil
}

func TestAgentLoopMCPIntegration_NormalCall(t *testing.T) {
	tools := map[string]func(args string) (string, error){
		"get_issue": func(args string) (string, error) {
			return "issue #123 is open", nil
		},
	}
	factory := createTestMCPFactory("mock-gh", tools)

	mgr := mcp.NewManagerWithFactory(nil, []string{"memory", "send_message"}, factory)

	ctx := context.Background()
	err := mgr.AddServer(ctx, mcp.ServerConfig{
		ID:      "github",
		Name:    "GitHub",
		Enabled: true,
		Transport: mcp.TransportConfig{
			Type:    mcp.TransportStdio,
			Command: "github-server",
		},
	})
	if err != nil {
		t.Fatalf("add server failed: %v", err)
	}

	// AddServer returns once configuration is registered/durable; startup is async.
	// Wait for the test runtime before asserting that round 1 receives its tools.
	srv, ok := mgr.GetServer("github")
	if !ok {
		t.Fatal("github MCP runtime was not registered")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Status() == mcp.StatusConnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Status() != mcp.StatusConnected {
		t.Fatalf("github MCP runtime did not connect: status=%s lastError=%q", srv.Status(), srv.LastError())
	}

	provider := &scriptedLLMProvider{
		responses: []*core.ChatResponse{
			// Round 1: calls the MCP tool
			{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: core.ToolCallFunction{
								Name:      "mcp__github__get_issue",
								Arguments: `{"id":"123"}`,
							},
						},
					},
				},
			},
			// Round 2: answers the user
			{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: "Issue 123 is currently open.",
				},
			},
		},
	}

	engine := &Engine{
		MaxIterations:  5,
		Provider:       provider,
		MCPManager:     mgr,
		ToolRegistry:   make(map[string]ToolExecutor),
		SessionManager: NewSessionManager(),
	}

	result := engine.Run("check issue 123")
	if result != "Issue 123 is currently open." {
		t.Fatalf("unexpected engine result: %q", result)
	}

	if provider.rounds != 2 {
		t.Fatalf("expected 2 rounds, got %d", provider.rounds)
	}

	// Verify Round 1 tools had mcp__github__get_issue
	round1Tools := provider.reqTools[0]
	found := false
	for _, tool := range round1Tools {
		if tool.Name == "mcp__github__get_issue" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mcp__github__get_issue was not exposed in round 1 tools")
	}
}

func TestAgentLoopMCPIntegration_ServerHotDisableAndDoubleCheck(t *testing.T) {
	tools := map[string]func(args string) (string, error){
		"get_issue": func(args string) (string, error) {
			return "issue #123 is open", nil
		},
	}
	factory := createTestMCPFactory("mock-gh", tools)

	mgr := mcp.NewManagerWithFactory(nil, []string{"memory"}, factory)

	ctx := context.Background()
	err := mgr.AddServer(ctx, mcp.ServerConfig{
		ID:      "github",
		Name:    "GitHub",
		Enabled: true,
		Transport: mcp.TransportConfig{
			Type:    mcp.TransportStdio,
			Command: "github-server",
		},
	})
	if err != nil {
		t.Fatalf("add server failed: %v", err)
	}

	provider := &scriptedLLMProvider{}
	provider.responses = []*core.ChatResponse{
		// Round 1: returns tool call, but in this round user disables the server
		{
			Message: core.ChatMessage{
				Role: core.RoleAssistant,
				ToolCalls: []core.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: core.ToolCallFunction{
							Name:      "mcp__github__get_issue",
							Arguments: `{}`,
						},
					},
				},
			},
		},
		// Round 2: LLM receives the disabled notice and tells the user
		{
			Message: core.ChatMessage{
				Role:    core.RoleAssistant,
				Content: "GitHub server is currently disabled, so I cannot fetch the issue.",
			},
		},
	}

	engine := &Engine{
		MaxIterations:  5,
		Provider:       provider,
		MCPManager:     mgr,
		ToolRegistry:   make(map[string]ToolExecutor),
		SessionManager: NewSessionManager(),
	}

	// Disable the server right before execution (simulating race)
	_ = mgr.SetServerEnabled(ctx, "github", false)

	res := engine.Run("check issue")
	if !strings.Contains(res, "disabled") {
		t.Fatalf("unexpected engine response: %q", res)
	}

	// In Round 2, tools must NOT contain mcp__github__get_issue because server was disabled!
	if len(provider.reqTools) >= 2 {
		round2Tools := provider.reqTools[1]
		for _, tool := range round2Tools {
			if tool.Name == "mcp__github__get_issue" {
				t.Fatalf("disabled mcp tool still present in round 2 tools: %s", tool.Name)
			}
		}
	}
}
