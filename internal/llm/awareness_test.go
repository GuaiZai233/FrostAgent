package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

func awarenessFixture(t *testing.T) (*Engine, *AwarenessService, *instanceconfig.Store, *[][]proactive.AwarenessMessage) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.env")
	if err := os.WriteFile(path, []byte("ENABLE_PROACTIVE_AWARENESS=true\nSYSTEM_PROMPT=Fixture persona\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := instanceconfig.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	t.Cleanup(func() { scope.Cancel(); scope.Wait() })
	e := &Engine{Scope: scope, SessionManager: NewSessionManager(scope), MaxIterations: 2}
	var sends [][]proactive.AwarenessMessage
	service := &AwarenessService{Engine: e, Resolve: func(AwarenessTarget) (AwarenessSender, bool) {
		return func(_ context.Context, _ AwarenessTarget, parts []proactive.AwarenessMessage, _ *SessionTurn) error {
			sends = append(sends, parts)
			return nil
		}, true
	}}
	e.Awareness = service
	return e, service, config, &sends
}

func TestAwarenessPrivateUsesHistoryWithoutInventingUserInput(t *testing.T) {
	e, service, _, sends := awarenessFixture(t)
	target := AwarenessTargetFor("fixture-platform", "private", "fixture-user")
	session := e.SessionManager.GetOrCreate(target.SessionID)
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "We are discussing a garden"})
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		if !strings.Contains(injectionText(req), proactive.AwarenessPrivatePrompt) || req.Messages[len(req.Messages)-1].Role != core.RoleSystem {
			t.Fatalf("missing system trigger: %+v", req.Messages)
		}
		if !strings.Contains(fmt.Sprint(req.Messages[0].Content), "Fixture persona") {
			t.Fatal("lost persona")
		}
		if len(req.Messages) != 3 || req.Messages[1].Content != "We are discussing a garden" {
			t.Fatalf("lost private history: %+v", req.Messages)
		}
		return finalInjection("How is the garden?"), nil
	})
	service.Trigger(e.Context(), target)
	if len(*sends) != 1 || (*sends)[0][0].Text != "How is the garden?" {
		t.Fatalf("missing send: %v", *sends)
	}
	history := session.Snapshot()
	if len(history) != 2 || history[1].Role != "assistant" || strings.Contains(fmt.Sprint(history[1].Content), "主动陪伴") {
		t.Fatalf("trigger polluted history: %+v", history)
	}
}

func TestAwarenessRechecksWhitelistAndResetBeforeSend(t *testing.T) {
	for _, action := range []string{"disable", "reset", "failure", "blocked", "locked"} {
		t.Run(action, func(t *testing.T) {
			e, service, config, sends := awarenessFixture(t)
			target := AwarenessTargetFor("fixture-platform", "private", "fixture-user")
			session := e.SessionManager.GetOrCreate(target.SessionID)
			if action == "locked" {
				e.Security = security.NewController(t.TempDir())
				principal, _ := security.NewPrincipal(target.Platform, target.ID)
				if err := e.Security.Access.Lock(principal, "fixture lock"); err != nil {
					t.Fatal(err)
				}
			}
			e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				switch action {
				case "disable":
					if err := config.Update(proactive.AwarenessEnabled, "false", false); err != nil {
						t.Fatal(err)
					}
				case "reset":
					if err := e.SessionManager.ResetSession(target.SessionID); err != nil {
						t.Fatal(err)
					}
				case "failure":
					return nil, errors.New("fixture provider failure")
				case "blocked":
					return injectionCalls("fixture-forbidden"), nil
				case "locked":
					t.Fatal("locked recipient reached provider")
				}
				return finalInjection("must not send"), nil
			})
			service.Trigger(e.Context(), target)
			if len(*sends) != 0 {
				t.Fatalf("%s sent a message", action)
			}
			if len(session.Snapshot()) != 0 {
				t.Fatalf("%s recorded unsent content", action)
			}
		})
	}
}

func TestAwarenessGroupUsesCompactWithoutPrivateHistoryOrBystanderActor(t *testing.T) {
	e, service, _, sends := awarenessFixture(t)
	target := AwarenessTargetFor("fixture-platform", "group", "fixture-group")
	session := e.SessionManager.GetOrCreate(target.SessionID)
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "irrelevant dialogue copy"})
	session.AppendGroupCompactMessage(GroupCompactMessage{Role: "user", SenderID: "fixture-member", Content: "discussing gardens"}, 100)
	e.Provider = injectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		text := injectionText(req)
		if !strings.Contains(text, proactive.AwarenessGroupPrompt) || !strings.Contains(text, "discussing gardens") || strings.Contains(text, "irrelevant dialogue copy") {
			t.Fatal(text)
		}
		run, _ := RunContextFromContext(ctx)
		if run.ActorUserID == "fixture-member" || run.ActorPlatform != "system" || run.OwnerType != memory.OwnerGroup {
			t.Fatalf("borrowed human authority: %+v", run)
		}
		return finalInjection("Let's talk gardens"), nil
	})
	service.Trigger(e.Context(), target)
	if len(*sends) != 1 {
		t.Fatal("group trigger did not send")
	}
	if got := session.SnapshotGroupContext(10, 12000, ""); len(got.RecentStructuredMessages) != 2 {
		t.Fatalf("confirmed assistant send missing from group context: %+v", got)
	}
}

func TestAwarenessToolMentionsAreCurrentGroupOnlyAndNotDuplicated(t *testing.T) {
	for _, member := range []string{"fixture-member", "unknown-member"} {
		t.Run(member, func(t *testing.T) {
			e, service, _, sends := awarenessFixture(t)
			target := AwarenessTargetFor("fixture-platform", "group", "fixture-group")
			session := e.SessionManager.GetOrCreate(target.SessionID)
			session.AppendGroupCompactMessage(GroupCompactMessage{Role: "user", SenderID: "fixture-member", Content: "gardens"}, 100)
			args := fmt.Sprintf(`{"messages":[{"type":"mention_user","mention_user_id":%q},{"type":"plain","text":"A garden thought"}]}`, member)
			tool := &proactiveMessageTool{structuredResultTool: structuredResultTool{staticTool: staticTool{name: "send_message"}, output: core.ToolExecutionResult{Content: args, MessagePayload: args}}}
			e.ToolRegistry = map[string]ToolExecutor{"send_message": tool}
			calls := 0
			e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				calls++
				if calls > 1 {
					return finalInjection(""), nil
				}
				for _, available := range req.Tools {
					if available.Name == "send_message" && strings.Contains(available.Description, "triggering message") {
						continue
					}
					if available.Name == "send_message" {
						t.Fatal("used ordinary proactive quote-only schema")
					}
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "fixture-call", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args}}}}}, nil
			})
			service.Trigger(e.Context(), target)
			want := 0
			if member == "fixture-member" {
				want = 1
			}
			if len(*sends) != want {
				t.Fatalf("member=%s sends=%+v", member, *sends)
			}
			if got := len(session.Snapshot()); got != want {
				t.Fatalf("confirmed history count=%d want=%d", got, want)
			}
		})
	}
}

func TestAwarenessWorkerTriggersWhitelistedPrivateWithoutIncomingMessage(t *testing.T) {
	e, service, config, _ := awarenessFixture(t)
	for key, value := range map[string]string{
		proactive.AwarenessInterval:                "0.001m - 0.001m",
		proactive.AwarenessPrivateWhitelistEnabled: "true", proactive.AwarenessPrivateWhitelist: "fixture-platform:fixture-user",
		proactive.AwarenessGroupWhitelistEnabled: "true",
	} {
		if err := config.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	sent := make(chan AwarenessTarget, 4)
	service.Resolve = func(target AwarenessTarget) (AwarenessSender, bool) {
		return func(_ context.Context, target AwarenessTarget, _ []proactive.AwarenessMessage, _ *SessionTurn) error {
			sent <- target
			return nil
		}, true
	}
	e.Provider = injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
		return finalInjection("How are you?"), nil
	})
	ctx, cancel := context.WithCancel(e.Context())
	done := make(chan struct{})
	e.Go(func() { defer close(done); service.Run(ctx) })
	select {
	case target := <-sent:
		if target.Kind != "private" || target.ID != "fixture-user" || target.Platform != "fixture-platform" {
			t.Fatalf("crossed whitelist: %+v", target)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timer did not trigger without inbound message")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop with runtime context")
	}
}

func TestAwarenessKeepsConfiguredCompactBufferAndIsolatesSkills(t *testing.T) {
	e, service, _, _ := awarenessFixture(t)
	e.GroupCompactor = &GroupCompactor{maxBufferSize: 300}
	target := AwarenessTargetFor("fixture-platform", "group", "fixture-group")
	session := e.SessionManager.GetOrCreate(target.SessionID)
	if err := session.ActivateSkill("fixture-human-skill", "fixture-revision", "private arguments", session.Epoch()); err != nil {
		t.Fatal(err)
	}
	for range 220 {
		session.AppendGroupCompactMessage(GroupCompactMessage{Role: "user", SenderID: "fixture-member", Content: "retained context"}, 300)
	}
	e.Provider = injectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		state := e.skillSession(ctx)
		if state == nil || state == session || len(state.skillSnapshot()) != 0 {
			t.Fatal("system trigger inherited shared skill state")
		}
		return finalInjection("A new thought"), nil
	})
	service.Trigger(e.Context(), target)
	if count := session.GroupCompactBufferCount(); count != 221 {
		t.Fatalf("configured compact buffer was truncated: %d", count)
	}
}
