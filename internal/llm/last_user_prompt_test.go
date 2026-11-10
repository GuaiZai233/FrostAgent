package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

// The trace must hold the user segment actually sent to the model, including
// adapter-injected response_context guidance that durable history strips.
func TestLastUserPromptCapturesRequestUserSegment(t *testing.T) {
	const key = "group:last-user-prompt"
	sm := NewSessionManager()
	session := sm.GetOrCreate(key)
	provider := &sequentialProvider{responses: []core.ChatResponse{
		{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "ok"}},
	}}
	engine := &Engine{MaxIterations: 3, Provider: provider, SessionManager: sm}

	userSegment := "User Message: hi\n\n<response_context>\n{\"guidance\":\"GUIDANCE_MARKER\"}\n</response_context>"
	ctx := WithRunContext(context.Background(), RunContext{SessionID: key, Owner: "owner", Epoch: session.Epoch()})
	engine.runLoopWithResult(ctx, []ChatMessage{{Role: "user", Content: userSegment}})

	got := session.LastUserPrompt()
	if !strings.Contains(got, "GUIDANCE_MARKER") || !strings.Contains(got, "User Message: hi") {
		t.Fatalf("last user prompt missing request segment: %q", got)
	}
}

func TestLastUserPromptClearedByResetAndRejectsStaleEpoch(t *testing.T) {
	session := NewSessionManager().GetOrCreate("group:last-user-reset")
	stale := session.Epoch()
	session.SetLastUserPromptAtEpoch(stale, "before reset")
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	if got := session.LastUserPrompt(); got != "" {
		t.Fatalf("reset kept user prompt: %q", got)
	}
	session.SetLastUserPromptAtEpoch(stale, "late write from old run")
	if got := session.LastUserPrompt(); got != "" {
		t.Fatalf("stale epoch repopulated prompt: %q", got)
	}
	session.SetLastUserPromptAtEpoch(session.Epoch(), "current run")
	if got := session.LastUserPrompt(); got != "current run" {
		t.Fatalf("current epoch write rejected: %q", got)
	}
}

func TestLastUserPromptCapturesMultimodalUserContent(t *testing.T) {
	const key = "group:last-user-multimodal"
	sm := NewSessionManager()
	session := sm.GetOrCreate(key)
	session.SetLastUserPromptAtEpoch(0, "previous request text")
	provider := &sequentialProvider{responses: []core.ChatResponse{
		{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "ok"}},
	}}
	engine := &Engine{MaxIterations: 3, Provider: provider, SessionManager: sm}
	parts := []core.ContentPart{
		{Type: string(core.ContentPartTypeText), Text: "看看这张图 MULTI_MARKER"},
		{Type: string(core.ContentPartTypeImage), ImageURL: &core.ImageURL{URL: "data:image/png;base64,AAAA"}},
	}
	ctx := WithRunContext(context.Background(), RunContext{SessionID: key, Owner: "owner", Epoch: session.Epoch()})
	engine.runLoopWithResult(ctx, []ChatMessage{{Role: "user", Content: parts}})

	got := session.LastUserPrompt()
	if strings.Contains(got, "previous request text") || !strings.Contains(got, "MULTI_MARKER") || !strings.Contains(got, "[图片]") {
		t.Fatalf("multimodal request not captured, or stale text kept: %q", got)
	}
}
