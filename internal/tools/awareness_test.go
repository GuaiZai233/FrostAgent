package tools

import (
	"context"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

func TestAwarenessSendToolRetainsCurrentConversationBoundary(t *testing.T) {
	tool := SendMsgTool()
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{Proactive: true, Awareness: true})
	for _, args := range []string{
		`{"messages":[{"type":"plain","text":"A new topic"}]}`,
		`{"messages":[{"type":"mention_user","mention_user_id":"fixture-member"},{"type":"plain","text":"A new topic"}]}`,
	} {
		result, err := tool.ExecuteResult(ctx, args)
		if err != nil || result.MessagePayload == "" {
			t.Fatalf("rejected current-session text/mention: %v %+v", err, result)
		}
	}
	for _, args := range []string{
		`{"session":"fixture-platform:private:other","messages":[{"type":"plain","text":"No"}]}`,
		`{"messages":[{"type":"quote","message_id":"fixture-message"},{"type":"plain","text":"No"}]}`,
		`{"messages":[{"type":"image","url":"https://example.invalid/image"}]}`,
	} {
		if _, err := tool.ExecuteResult(ctx, args); err == nil {
			t.Fatalf("accepted unsafe timer send: %s", args)
		}
	}
	ordinary := llm.WithRunContext(context.Background(), llm.RunContext{Proactive: true})
	if _, err := tool.ExecuteResult(ordinary, `{"messages":[{"type":"plain","text":"No"}]}`); err == nil {
		t.Fatal("expanded ordinary proactive permissions")
	}
}
