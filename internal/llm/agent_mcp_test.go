package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/mcp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type mockMCPTestingTransport struct {
	mu           sync.Mutex
	roundTripFn  func(ctx context.Context, req *mcp.JSONRPCRequest) (*mcp.JSONRPCResponse, error)
	notifyFn     func(ctx context.Context, notif *mcp.JSONRPCNotification) error
	closeFn      func() error
}

func (m *mockMCPTestingTransport) RoundTrip(ctx context.Context, req *mcp.JSONRPCRequest) (*mcp.JSONRPCResponse, error) {
	m.mu.Lock()
	fn := m.roundTripFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockMCPTestingTransport) Notify(ctx context.Context, notif *mcp.JSONRPCNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notifyFn != nil {
		return m.notifyFn(ctx, notif)
	}
	return nil
}

func (m *mockMCPTestingTransport) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
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
	mockTransport := &mockMCPTestingTransport{
		roundTripFn: func(ctx context.Context, req *mcp.JSONRPCRequest) (*mcp.JSONRPCResponse, error) {
			switch req.Method {
			case "initialize":
				res, _ := json.Marshal(mcp.InitializeResult{
					ProtocolVersion: mcp.ProtocolVersion,
					ServerInfo:      mcp.ServerInfo{Name: "mock-gh", Version: "1.0"},
				})
				return &mcp.JSONRPCResponse{ID: req.ID, Result: res}, nil
			case "tools/list":
				res, _ := json.Marshal(mcp.ToolListResult{
					Tools: []mcp.MCPToolDefinition{
						{
							Name:        "get_issue",
							Description: "Fetch issue info",
							InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
						},
					},
				})
				return &mcp.JSONRPCResponse{ID: req.ID, Result: res}, nil
			case "tools/call":
				res, _ := json.Marshal(mcp.ToolCallResult{
					Content: []mcp.ContentBlock{
						{Type: "text", Text: "issue #123 is open"},
					},
				})
				return &mcp.JSONRPCResponse{ID: req.ID, Result: res}, nil
			default:
				return nil, fmt.Errorf("unexpected method: %s", req.Method)
			}
		},
	}

	mgr := mcp.NewManagerWithFactory(nil, []string{"memory", "send_message"}, func(cfg mcp.TransportConfig) (mcp.Transport, error) {
		return mockTransport, nil
	})

	ctx := context.Background()
	err := mgr.AddServer(ctx, mcp.ServerConfig{
		ID:      "github",
		Name:    "GitHub",
		Enabled: true,
		Transport: mcp.TransportConfig{
			Type: mcp.TransportStdio,
		},
	})
	if err != nil {
		t.Fatalf("add server failed: %v", err)
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
	mockTransport := &mockMCPTestingTransport{
		roundTripFn: func(ctx context.Context, req *mcp.JSONRPCRequest) (*mcp.JSONRPCResponse, error) {
			switch req.Method {
			case "initialize":
				res, _ := json.Marshal(mcp.InitializeResult{
					ProtocolVersion: mcp.ProtocolVersion,
					ServerInfo:      mcp.ServerInfo{Name: "mock-gh", Version: "1.0"},
				})
				return &mcp.JSONRPCResponse{ID: req.ID, Result: res}, nil
			case "tools/list":
				res, _ := json.Marshal(mcp.ToolListResult{
					Tools: []mcp.MCPToolDefinition{
						{
							Name:        "get_issue",
							Description: "Fetch issue info",
							InputSchema: json.RawMessage(`{}`),
						},
					},
				})
				return &mcp.JSONRPCResponse{ID: req.ID, Result: res}, nil
			default:
				return nil, fmt.Errorf("unexpected method: %s", req.Method)
			}
		},
	}

	mgr := mcp.NewManagerWithFactory(nil, []string{"memory"}, func(cfg mcp.TransportConfig) (mcp.Transport, error) {
		return mockTransport, nil
	})

	ctx := context.Background()
	err := mgr.AddServer(ctx, mcp.ServerConfig{
		ID:      "github",
		Name:    "GitHub",
		Enabled: true,
		Transport: mcp.TransportConfig{
			Type: mcp.TransportStdio,
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
