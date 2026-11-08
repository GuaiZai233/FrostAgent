package botstatus

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

// Direct Chat (mock) sessions must stay out of the session list, the overview
// count and the session inspector. Hiding is driven by the trusted run flag,
// never by the session ID, so real sessions with a "mock:" prefix stay visible.
func TestMockSessionsHiddenFromSessionInspection(t *testing.T) {
	manager := llm.NewSessionManager()
	svc := New(&llm.Engine{SessionManager: manager}, "test", "")

	mockID := "mock:gen-test:onebot:private:mock-user-1"
	mock := manager.GetOrCreate(mockID)
	mock.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "mock input"})
	mock.SetLastPromptTrace("mock persona", "mock-model")
	mock.MarkMock()

	spoofID := "mock:private:123"
	spoof := manager.GetOrCreate(spoofID)
	spoof.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "real spoofed-prefix input"})

	real := manager.GetOrCreate("private:test_real_user")
	real.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "real input"})

	if got := manager.CountVisible(); got != 2 {
		t.Fatalf("CountVisible = %d, want 2 (mock excluded)", got)
	}

	list, err := svc.GetSessions(context.Background(), connect.NewRequest(&v1.GetSessionsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, info := range list.Msg.Sessions {
		seen[info.SessionId] = true
	}
	if seen[mockID] {
		t.Fatalf("mock session leaked into session list")
	}
	if !seen[spoofID] || !seen["private:test_real_user"] {
		t.Fatalf("real sessions missing from list: %v", seen)
	}

	ctxResp, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: mockID, RecentLimit: 50}))
	if err != nil {
		t.Fatal(err)
	}
	if ctxResp.Msg.SystemPrompt != "" || ctxResp.Msg.Model != "" || len(ctxResp.Msg.History) != 0 || len(ctxResp.Msg.RecentStructuredMessages) != 0 {
		t.Fatalf("mock session exposed through inspector: %+v", ctxResp.Msg)
	}

	spoofResp, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: spoofID, RecentLimit: 50}))
	if err != nil {
		t.Fatal(err)
	}
	if len(spoofResp.Msg.History) != 1 {
		t.Fatalf("real session with mock: prefix must remain inspectable: %+v", spoofResp.Msg)
	}
}
