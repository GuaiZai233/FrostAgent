package llm

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/skills"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageGuidanceRefreshAndTrace(t *testing.T) {
	wantGuidance := "<frostagent_msg_guidance>\n" +
		"为保证拟真的对话效果，你被允许且被鼓励多段回复用户消息。在正常回复中插入<FROSTAGENT_SEGMENT_MARK>，FrostAgent 后端会自动解析并分段将你的消息发出。请根据语境决定是否这样输出。\n" +
		"如因特殊需求，你想完整输出 mark 的整个原文，请用反引号``包裹它，这样后端不会解析。\n" +
		"</frostagent_msg_guidance>"
	if messageGuidance != wantGuidance {
		t.Fatalf("unexpected guidance: %s", messageGuidance)
	}
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	provider := &mockTraceProvider{}
	manager := NewSessionManager()
	session := manager.GetOrCreate("synthetic-segment-session")
	e := &Engine{Scope: runtimescope.New(config, nil, nil), Provider: provider, MaxIterations: 1, SessionManager: manager, Skills: skills.NewStore(db, "synthetic-instance")}
	defer func() { e.Scope.Cancel(); e.Scope.Wait() }()
	base := []ChatMessage{{Role: "system", Content: "primary instructions"}, {Role: "user", Content: "hello"}}
	ctx := withRunContext(context.Background(), RunContext{SessionID: session.ID()})
	for _, enabled := range []bool{false, true, true, false} {
		value := "false"
		if enabled {
			value = "true"
		}
		if err := config.Update(replyformat.EnvKey, value, false); err != nil {
			t.Fatal(err)
		}
		messages, err := e.skillMessages(ctx, base)
		if err != nil {
			t.Fatal(err)
		}
		messages = e.messageGuidanceMessages(messages)
		if enabled && (messages[1].Content != messageGuidance || !strings.HasPrefix(messages[2].Content.(string), skillsContextPrefix)) {
			t.Fatalf("wrong ordering: %+v", messages)
		}
		result := e.RunMessagesWithContext(messages, RunContext{SessionID: session.ID()})
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		prompt, _ := session.LastPromptTrace()
		want := 0
		if enabled {
			want = 1
		}
		if strings.Count(prompt, "<frostagent_msg_guidance>") != want {
			t.Fatalf("stale/duplicate guidance: %s", prompt)
		}
		if enabled && strings.Index(prompt, "<frostagent_msg_guidance>") > strings.Index(prompt, skillsContextPrefix) {
			t.Fatal("guidance follows skills")
		}
		base = messages
	}
	if len(session.Snapshot()) != 0 {
		t.Fatal("transient guidance persisted")
	}
	// No skill store is also supported; explicit system messages cannot skip it.
	e.Skills = nil
	if err := config.Update(replyformat.EnvKey, "true", false); err != nil {
		t.Fatal(err)
	}
	e.RunMessages([]ChatMessage{{Role: "system", Content: "custom"}, {Role: "user", Content: "hi"}})
	if provider.lastRequest.Messages[1].Content != messageGuidance {
		t.Fatal("missing guidance without skills")
	}
}
