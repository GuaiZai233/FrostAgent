package admincmd

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/groupsummary"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
)

func TestCompactAsyncErrorsExposeOnlySummaryAndReference(t *testing.T) {
	const raw = "Post https://endpoint.invalid/private: synthetic-secret"
	for _, group := range []bool{false, true} {
		name, summary := "private", "私聊上下文压缩总结失败"
		if group {
			name, summary = "group", "群聊上下文压缩总结失败"
		}
		t.Run(name, func(t *testing.T) {
			store := logs.New("command-instance", "test", 50)
			scope := runtimescope.New(nil, nil, store)
			t.Cleanup(func() { scope.Cancel(); scope.Wait() })
			provider := &mockLLM{err: errors.New(raw)}
			engine := &llm.Engine{Scope: scope, SessionManager: llm.NewSessionManager(), Provider: provider, ModelName: "test-model"}
			if group {
				summaries, err := groupsummary.NewStore(filepath.Join(t.TempDir(), "summaries.json"))
				if err != nil {
					t.Fatal(err)
				}
				engine.GroupCompactor = llm.NewGroupCompactor(provider, summaries, "test-model", 10, time.Second)
				engine.GroupCompactor.Scope = scope
				t.Cleanup(engine.GroupCompactor.Close)
			}
			const sessionID = "test:synthetic-session"
			session := engine.SessionManager.GetOrCreate(sessionID)
			if group {
				session.AppendGroupCompactMessage(llm.GroupCompactMessage{Content: "synthetic content"}, 10)
			} else {
				session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "synthetic question"})
				session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "synthetic answer"})
			}
			replies := make(chan string, 2)
			cmdCtx := CommandContext{SessionID: sessionID, IsGroup: group, Owner: "test:synthetic-owner",
				RouteScope: modelrouter.Scope{Platform: "test", GroupID: "synthetic-group"},
				Reply:      func(_ context.Context, text string, _ bool) error { replies <- text; return nil }}
			if err := NewExecutor(engine).Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdCompact}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				select {
				case reply := <-replies:
					if i == 0 {
						continue
					}
					prefix := "[FrostAgent 错误] " + summary + " (Ref_ID: "
					if !strings.HasPrefix(reply, prefix) || strings.Contains(reply, "endpoint.invalid") || strings.Contains(reply, "synthetic-secret") {
						t.Fatalf("unsafe command failure: %q", reply)
					}
					id := strings.TrimSuffix(strings.TrimPrefix(reply, prefix), ")")
					entry, found := store.FindError(id)
					if !found || entry.TraceID != sessionID || !strings.Contains(entry.Content, raw) {
						t.Fatalf("missing command diagnostics: %+v", entry)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("command did not complete")
				}
			}
		})
	}
}
