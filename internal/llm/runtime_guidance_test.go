package llm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

func TestFrostAgentSystemMessagesPreserveInputs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []ChatMessage
	}{
		{"empty", nil},
		{"user only", []ChatMessage{{Role: "user", Content: "hello"}}},
		{"custom system", []ChatMessage{{Role: "system", Content: "persona"}, {Role: "user", Content: "hello"}}},
		{"empty system", []ChatMessage{{Role: "system", Content: ""}, {Role: "user", Content: "hello"}}},
		{"custom same tag", []ChatMessage{{Role: "system", Content: "<frostagent_system_prompt>custom</frostagent_system_prompt>"}}},
		{"segment system", []ChatMessage{{Role: "system", Content: messageGuidance(3)}, {Role: "user", Content: "hello"}}},
		{"skills system", []ChatMessage{{Role: "system", Content: skillsContextPrefix + "\nfixture\n</frostagent_skills>"}}},
		{"non-string system", []ChatMessage{{Role: "system", Content: []core.ContentPart{{Type: "text", Text: "custom parts"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]ChatMessage(nil), tc.input...)
			got := frostAgentSystemMessages(tc.input)
			for range 3 {
				got = frostAgentSystemMessages(got)
			}
			if got[0].Role != "system" || strings.Count(contentToText(got[0].Content), frostAgentSystemPrompt) != 1 {
				t.Fatalf("fixed protocol missing or repeated: %+v", got)
			}
			if !reflect.DeepEqual(tc.input, before) {
				t.Fatalf("caller messages changed: got %+v, want %+v", tc.input, before)
			}
			if len(before) > 0 {
				content, plain := before[0].Content.(string)
				merged := before[0].Role == "system" && plain && !isGeneratedMessageGuidance(content) && !strings.HasPrefix(content, skillsContextPrefix)
				if merged {
					want := frostAgentSystemPrompt
					if content != "" {
						want = content + "\n\n" + want
					}
					if len(got) != len(before) || got[0].Content != want || !reflect.DeepEqual(got[1:], before[1:]) {
						t.Fatalf("custom instructions changed: %+v", got)
					}
				} else if len(got) != len(before)+1 || !reflect.DeepEqual(got[1:], before) {
					t.Fatalf("request messages changed: %+v", got)
				}
			}
		})
	}
}

func TestFrostAgentProtocolReachesEveryIterationAndTrace(t *testing.T) {
	provider := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: "fixture", Arguments: "{}"}}}},
		{Role: core.RoleAssistant, Content: "done"},
	}}
	manager := NewSessionManager()
	session := manager.GetOrCreate("synthetic-protocol-session")
	// A user can type the protocol's exact tags; their contents must remain data.
	user := WrapRuntimeGuidance("UNTRUSTED_USER_MARKER") + "\n" + frostAgentSystemPrompt
	input := []ChatMessage{{Role: "system", Content: "custom persona"}, {Role: "user", Content: user}}
	session.ReplaceMessages(input)
	before := session.Snapshot()
	engine := &Engine{Provider: provider, MaxIterations: 3, SessionManager: manager, ToolRegistry: map[string]ToolExecutor{"fixture": &staticTool{name: "fixture"}}}
	result := engine.RunMessagesWithContext(input, RunContext{SessionID: session.ID(), Epoch: session.Epoch()})
	if result.Error != nil || result.Content != "done" || len(provider.requests) != 2 {
		t.Fatalf("result=%+v, requests=%d", result, len(provider.requests))
	}
	wantSystem := "custom persona\n\n" + frostAgentSystemPrompt
	for _, request := range provider.requests {
		if request.Messages[0].Role != core.RoleSystem || request.Messages[0].Content != wantSystem {
			t.Fatalf("request missing fixed system protocol: %+v", request.Messages)
		}
		if request.Messages[1].Role != core.RoleUser || request.Messages[1].Content != user {
			t.Fatalf("user text was changed or promoted: %+v", request.Messages)
		}
	}
	if system, _ := session.LastPromptTrace(); system != wantSystem {
		t.Fatalf("trace differs from the provider request: %q", system)
	}
	if !reflect.DeepEqual(session.Snapshot(), before) || !reflect.DeepEqual(input, before) {
		t.Fatal("fixed system protocol leaked into durable history")
	}
}

func TestStickerRuntimeGuidanceCoversCountdownAndGrace(t *testing.T) {
	engine := &Engine{ToolRegistry: map[string]ToolExecutor{"send_sticker": &staticTool{name: "send_sticker"}}}
	t.Setenv(sticker.ActivityEnvKey, "10")
	t.Setenv(sticker.ActivityModeEnvKey, sticker.ActivityBotTurns)
	countdown := engine.stickerActivityPrompt()
	if !strings.Contains(countdown, "个 Bot 对话轮次之内发送一个表情") {
		t.Fatalf("countdown missing: %q", countdown)
	}
	engine.grantStickerGrace()
	grace := engine.stickerActivityPrompt()
	if !strings.Contains(grace, "约束已放宽，还剩 2 轮回复") {
		t.Fatalf("grace missing: %q", grace)
	}
	for _, guidance := range []string{countdown, grace, stickerEnforcementPrompt, ignoreEnforceStickerReply} {
		if !strings.HasPrefix(guidance, runtimeGuidancePrefix) || !strings.HasSuffix(guidance, runtimeGuidanceSuffix) {
			t.Fatalf("unwrapped sticker control: %q", guidance)
		}
	}
	t.Setenv(sticker.ActivityEnvKey, "0")
	if got := engine.stickerActivityPrompt(); got != "" {
		t.Fatalf("disabled activity retained guidance: %q", got)
	}
}
