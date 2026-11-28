package llm

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
)

type namedTool struct {
	name  string
	calls *int
}

func (t namedTool) Name() string               { return t.name }
func (t namedTool) Description() string        { return "synthetic " + t.name }
func (t namedTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (t namedTool) Execute(string) (string, error) {
	*t.calls++
	return "ok", nil
}

// scriptedProvider records every request and answers from a script.
type scriptedProvider struct {
	mu       sync.Mutex
	requests []core.ChatRequest
	script   []core.ChatMessage
}

func (p *scriptedProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	reply := core.ChatMessage{Role: core.RoleAssistant, Content: "reply"}
	if len(p.requests) <= len(p.script) {
		reply = p.script[len(p.requests)-1]
	}
	return &core.ChatResponse{Message: reply}, nil
}

func (p *scriptedProvider) first() core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[0]
}

func toolNames(req core.ChatRequest) map[string]bool {
	names := map[string]bool{}
	for _, tool := range req.Tools {
		names[tool.Name] = true
	}
	return names
}

func systemText(req core.ChatRequest) string {
	var parts []string
	for _, message := range req.Messages {
		if message.Role == core.RoleSystem {
			if text, ok := message.Content.(string); ok {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func newTempChatEngine(t *testing.T, provider core.LLMProvider, calls *int) *Engine {
	t.Helper()
	t.Setenv("SYSTEM_PROMPT", "SYNTH-SYSTEM-PROMPT")
	dir := t.TempDir()
	store := memory.NewStore(filepath.Join(dir, "brain.json"))
	catalog := memory.NewCatalogStore(filepath.Join(dir, "catalog.json"))
	if err := store.Save(memory.MemoryEntry{ID: "m1", Owner: "alice", Content: "SYNTH-RECALLED-FACT", Tags: []string{"probe"}, Source: memory.SourceManual}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Replace(memory.UserMemoryCatalog{Owner: "alice", Topics: []memory.MemoryTopic{{Name: "SYNTH-CATALOG-TOPIC"}}}); err != nil {
		t.Fatal(err)
	}
	tools := map[string]ToolExecutor{}
	for _, name := range []string{"memory", "search_chat_history", "read_tool_history", "calculator"} {
		tools[name] = namedTool{name: name, calls: calls}
	}
	return &Engine{
		Provider:       provider,
		ToolRegistry:   tools,
		MaxIterations:  3,
		DialoguePrompt: "SYNTH-PERSONA-EXAMPLE",
		MemoryReader:   memory.NewReader(store, 5),
		MemoryCatalog:  catalog,
		MemoryGateway:  memory.NewGateway(),
		SessionManager: NewSessionManager(),
	}
}

func TestTempChatPromptOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options *TempChat
		persona bool // SYSTEM_PROMPT and persona dialogue present
		catalog bool // memory catalog index present
		recall  bool // recalled memory entries present
	}{
		{"ordinary", nil, true, true, true},
		{"bare tempchat", &TempChat{}, true, true, true},
		{"no personality", &TempChat{NoPersonality: true}, false, false, true},
		{"no memory", &TempChat{NoMemory: true}, true, false, false},
		{"no personality and no memory", &TempChat{NoPersonality: true, NoMemory: true}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			provider := &scriptedProvider{}
			engine := newTempChatEngine(t, provider, &calls)
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "probe"}}, RunContext{
				Owner: "alice", ActorUserID: "alice", ActorPlatform: "qq", TempChat: tc.options,
			})
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			system := systemText(provider.first())
			for marker, want := range map[string]bool{
				"SYNTH-SYSTEM-PROMPT":   tc.persona,
				"SYNTH-PERSONA-EXAMPLE": tc.persona,
				"SYNTH-CATALOG-TOPIC":   tc.catalog,
				"SYNTH-RECALLED-FACT":   tc.recall,
			} {
				if got := strings.Contains(system, marker); got != want {
					t.Errorf("%s present=%v, want %v in %q", marker, got, want, system)
				}
			}
			if !strings.Contains(system, "frostagent_system_prompt") {
				t.Error("the backend protocol must survive -np")
			}
		})
	}
}

func TestTempChatToolsAndExecution(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options *TempChat
		want    map[string]bool
	}{
		{"ordinary", nil, map[string]bool{"memory": true, "search_chat_history": true, "read_tool_history": true, "calculator": true}},
		{"bare tempchat", &TempChat{}, map[string]bool{"search_chat_history": true, "read_tool_history": true, "calculator": true}},
		{"no personality", &TempChat{NoPersonality: true}, map[string]bool{"search_chat_history": true, "read_tool_history": true, "calculator": true}},
		{"no memory", &TempChat{NoMemory: true}, map[string]bool{"calculator": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			provider := &scriptedProvider{script: []core.ChatMessage{{
				Role: core.RoleAssistant,
				ToolCalls: []core.ToolCall{
					{ID: "c1", Type: "function", Function: core.ToolCallFunction{Name: "memory", Arguments: `{}`}},
					{ID: "c2", Type: "function", Function: core.ToolCallFunction{Name: "calculator", Arguments: `{}`}},
				},
			}}}
			engine := newTempChatEngine(t, provider, &calls)
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "probe"}}, RunContext{
				Owner: "alice", ActorUserID: "alice", ActorPlatform: "qq", TempChat: tc.options,
			})
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			got := toolNames(provider.first())
			for _, name := range []string{"memory", "search_chat_history", "read_tool_history", "calculator"} {
				if got[name] != tc.want[name] {
					t.Errorf("tool %s offered=%v, want %v", name, got[name], tc.want[name])
				}
			}
			// A model that names a withheld tool anyway must not run it.
			wantCalls := 2
			if tc.options != nil {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Errorf("executed %d tools, want %d", calls, wantCalls)
			}
		})
	}
}

func TestTempChatRecordsNothing(t *testing.T) {
	var calls int
	provider := &scriptedProvider{}
	engine := newTempChatEngine(t, provider, &calls)
	session := engine.SessionManager.GetOrCreate("private:alice")
	session.AddMessageAtEpoch(core.ChatMessage{Role: core.RoleUser, Content: "earlier"}, 0)
	session.AddMessageAtEpoch(core.ChatMessage{Role: core.RoleAssistant, Content: "earlier reply"}, 0)

	withHistory := session.EphemeralMessages(true, "now")
	if len(withHistory) != 3 || withHistory[0].Content != "earlier" || withHistory[2].Content != "now" {
		t.Fatalf("unexpected snapshot: %+v", withHistory)
	}
	if only := session.EphemeralMessages(false, "now"); len(only) != 1 || only[0].Content != "now" {
		t.Fatalf("history leaked into -nm snapshot: %+v", only)
	}
	// The snapshot is private: editing it must not reach the session.
	withHistory[0].Content = "tampered"

	result := engine.RunMessagesWithContext(withHistory, RunContext{
		SessionID: "private:alice", Owner: "alice", ActorUserID: "alice", ActorPlatform: "qq",
		TempChat: &TempChat{}, Epoch: session.Epoch(),
	})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	history := session.Snapshot()
	if len(history) != 2 || history[0].Content != "earlier" || history[1].Content != "earlier reply" {
		t.Fatalf("temporary conversation changed session history: %+v", history)
	}
	if system, _ := session.LastPromptTrace(); system != "" {
		t.Fatalf("temporary conversation published a prompt trace: %q", system)
	}
}

func TestTransientReplyLogNeverTouchesSessions(t *testing.T) {
	log := NewTransientReplyLog()
	defer log.Finish(false)
	log.Record("delivered text", func(bool) { t.Error("transient log must not stage group compaction") })
	log.NoteConfirmed()
	log.RecordFinalProgress("delivered text", nil)
	log.CommitDelivered()
	if log.CommitFinal("delivered text", false) {
		t.Fatal("transient log must never admit a final commit")
	}
	if log.Text() != "delivered text" || log.SpokeText() != "delivered text" || !log.Confirmed() {
		t.Fatalf("delivery facts lost: %q", log.Text())
	}
}

func TestTempChatQuotedGuidance(t *testing.T) {
	for _, tc := range []struct {
		mark logs.TempChatMark
		want string
	}{
		{logs.TempChatMark{}, "用户引用的消息，为 Assistant 处于“临时对话”下所产生的，内容原则上不影响正常消息队列，仅供参考。"},
		{logs.TempChatMark{NoPersonality: true}, "用户引用的消息，为 Assistant 处于“临时对话”下所产生的，内容原则上不影响正常消息队列，仅供参考。此次临时对话(禁用了人格提示词)"},
		{logs.TempChatMark{NoMemory: true}, "用户引用的消息，为 Assistant 处于“临时对话”下所产生的，内容原则上不影响正常消息队列，仅供参考。此次临时对话(禁用了记忆)"},
		{logs.TempChatMark{NoPersonality: true, NoMemory: true}, "用户引用的消息，为 Assistant 处于“临时对话”下所产生的，内容原则上不影响正常消息队列，仅供参考。此次临时对话(禁用了人格提示词、禁用了记忆)"},
	} {
		if got := TempChatQuotedGuidance(tc.mark); got != tc.want {
			t.Errorf("mark %+v: got %q, want %q", tc.mark, got, tc.want)
		}
	}
}

func TestTempChatNeverUsesSharedSkillState(t *testing.T) {
	engine := &Engine{SessionManager: NewSessionManager()}
	session := engine.SessionManager.GetOrCreate("private:alice")
	ordinary := withRunContext(context.Background(), RunContext{SessionID: "private:alice"})
	if engine.skillSession(ordinary) != session {
		t.Fatal("ordinary runs must keep using the session's skill state")
	}
	temporary := withRunContext(context.Background(), RunContext{SessionID: "private:alice", TempChat: &TempChat{}})
	if engine.skillSession(temporary) != nil {
		t.Fatal("temporary conversation reached the shared session skill state")
	}
}
