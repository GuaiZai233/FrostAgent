package llm

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockBillingLLMProvider struct {
	mu       sync.Mutex
	reqCount int
}

func (m *mockBillingLLMProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqCount++
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "Billing test response",
		},
		Usage: &core.Usage{
			PromptTokens:     120,
			CompletionTokens: 30,
			TotalTokens:      150,
		},
	}, nil
}

func TestAgent_BillingReservationAndCommit(t *testing.T) {
	var (
		mu                 sync.Mutex
		receivedReserveReq billing.LLMReserveRequest
		receivedKey        string
		commitCalled       bool
		committedActual    int64
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/billing/llm/reservations" && r.Method == http.MethodPost:
			receivedKey = r.Header.Get("Idempotency-Key")
			_ = json.NewDecoder(r.Body).Decode(&receivedReserveReq)

			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": billing.LLMReservationResult{
					ReservationID: "res_mock_123",
					Decision:      billing.DecisionReserved,
					Status:        billing.StatusReserved,
					ReservedMinor: receivedReserveReq.AmountMinor,
					BalanceMinor:  5000,
				},
			})
		case strings.HasPrefix(r.URL.Path, "/v1/billing/llm/reservations/") && strings.HasSuffix(r.URL.Path, "/commit"):
			commitCalled = true
			var req map[string]int64
			_ = json.NewDecoder(r.Body).Decode(&req)
			committedActual = req["actual_minor"]

			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": billing.LLMReservationResult{
					ReservationID: "res_mock_123",
					Status:        billing.StatusCommitted,
					BalanceMinor:  4900,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	bClient := billing.NewClient(server.URL, "test-token", 2*time.Second)
	bConfig := billing.Config{
		Enabled:          true,
		Timeout:          2 * time.Second,
		MaxOutputTokens:  1024,
		SafetyMultiplier: 1.2,
	}

	engine := &Engine{
		MaxIterations:  3,
		ToolRegistry:   make(map[string]ToolExecutor),
		Provider:       &mockBillingLLMProvider{},
		ModelName:      "claude-3-5-sonnet",
		BillingClient:  bClient,
		BillingConfig:  bConfig,
		SessionManager: NewSessionManager(),
	}

	// 1. Test when Platform and TaskID are empty (should safely default to qq and generate valid taskID/idempotency key)
	runCtx := RunContext{
		Billing: &BillingRunState{
			Platform:      "",
			ExternalID:    "12345678",
			DisplayName:   "FoxUser",
			TaskID:        "",
			BillingActive: true,
		},
	}

	res := engine.RunMessagesWithContext([]ChatMessage{
		{Role: "user", Content: "Hello with billing"},
	}, runCtx)

	if res.Error != nil {
		t.Fatalf("unexpected agent error: %v", res.Error)
	}
	if res.Content != "Billing test response" {
		t.Errorf("unexpected content: %s", res.Content)
	}

	mu.Lock()
	if receivedReserveReq.Platform != "qq" {
		t.Errorf("expected platform='qq', got %q", receivedReserveReq.Platform)
	}
	if !strings.HasPrefix(receivedReserveReq.TaskID, "qq_12345678_") {
		t.Errorf("expected taskID starting with 'qq_12345678_', got %q", receivedReserveReq.TaskID)
	}
	if !strings.HasPrefix(receivedKey, "res_qq_qq_12345678_") {
		t.Errorf("expected Idempotency-Key starting with 'res_qq_qq_12345678_', got %q", receivedKey)
	}
	if !commitCalled {
		t.Errorf("expected billing reservation to be committed")
	}
	if committedActual <= 0 {
		t.Errorf("expected actual committed tokens > 0, got %d", committedActual)
	}
	mu.Unlock()

	// 2. Test when Platform is astrbot and TaskID is specified
	mu.Lock()
	commitCalled = false
	receivedKey = ""
	receivedReserveReq = billing.LLMReserveRequest{}
	mu.Unlock()

	runCtx2 := RunContext{
		Billing: &BillingRunState{
			Platform:      "astrbot",
			ExternalID:    "ast_user_001",
			DisplayName:   "AstrMember",
			TaskID:        "ast_msg_999",
			BillingActive: true,
		},
	}

	res2 := engine.RunMessagesWithContext([]ChatMessage{
		{Role: "user", Content: "Hello from astrbot"},
	}, runCtx2)

	if res2.Error != nil {
		t.Fatalf("unexpected agent error: %v", res2.Error)
	}

	mu.Lock()
	if receivedReserveReq.Platform != "astrbot" {
		t.Errorf("expected platform='astrbot', got %q", receivedReserveReq.Platform)
	}
	if receivedReserveReq.TaskID != "ast_msg_999" {
		t.Errorf("expected taskID='ast_msg_999', got %q", receivedReserveReq.TaskID)
	}
	if receivedKey != "res_astrbot_ast_msg_999_0" {
		t.Errorf("expected Idempotency-Key='res_astrbot_ast_msg_999_0', got %q", receivedKey)
	}
	if !commitCalled {
		t.Errorf("expected commit to be called for astrbot session")
	}
	mu.Unlock()
}
