package botstatus

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

// Direct Chat (mock) sessions must stay out of the session list and the
// session inspector, including their last prompt trace.
func TestMockSessionsHiddenFromSessionInspection(t *testing.T) {
	manager := llm.NewSessionManager()
	svc := New(&llm.Engine{SessionManager: manager}, "test", "")

	mockID := "mock:gen-test:onebot:private:mock-user-1"
	mock := manager.GetOrCreate(mockID)
	mock.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "mock input"})
	mock.SetLastPromptTrace("mock persona", "mock-model")

	real := manager.GetOrCreate("private:test_real_user")
	real.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "real input"})

	list, err := svc.GetSessions(context.Background(), connect.NewRequest(&v1.GetSessionsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range list.Msg.Sessions {
		if strings.HasPrefix(info.SessionId, "mock:") {
			t.Fatalf("mock session leaked into session list: %s", info.SessionId)
		}
	}

	ctxResp, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: mockID, RecentLimit: 50}))
	if err != nil {
		t.Fatal(err)
	}
	if ctxResp.Msg.SystemPrompt != "" || ctxResp.Msg.Model != "" || len(ctxResp.Msg.History) != 0 || len(ctxResp.Msg.RecentStructuredMessages) != 0 {
		t.Fatalf("mock session exposed through inspector: %+v", ctxResp.Msg)
	}

	realResp, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: "private:test_real_user", RecentLimit: 50}))
	if err != nil {
		t.Fatal(err)
	}
	if len(realResp.Msg.History) != 1 {
		t.Fatalf("real session should remain inspectable: %+v", realResp.Msg)
	}
}
