package llm

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/memory"
	"FrostAgent/internal/security"
)

func TestBuildContextSystemPromptStablePrefixAcrossMinutes(t *testing.T) {
	now := time.Date(2026, time.October, 12, 18, 43, 0, 0, time.FixedZone("CST", 8*60*60))
	for _, tc := range []struct {
		name, system, dialogue, prefix string
	}{
		{"system and persona", "固定设定", "固定人设", "固定设定\n\n固定人设\n\n"},
		{"system only", "固定设定", "", "固定设定\n\n"},
		{"persona only", "", "固定人设", "固定人设\n\n"},
		{"no stable instructions", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, instant := range []time.Time{now, now.Add(time.Minute)} {
				prompt := buildContextSystemPrompt(tc.system, tc.dialogue, instant)
				prefix, label, ok := strings.Cut(prompt, "当前系统时间：")
				if !ok || prefix != tc.prefix {
					t.Fatalf("stable prefix changed: got %q, want %q", prefix, tc.prefix)
				}
				if label != memory.CurrentTimeLabel(instant) {
					t.Fatalf("current time missing or stale: got %q, want %q", label, memory.CurrentTimeLabel(instant))
				}
			}
		})
	}
}

func TestEngineContextPromptMemoryIsolation(t *testing.T) {
	const system = "固定系统设定"
	const dialogue = "固定人设示例"
	t.Setenv("SYSTEM_PROMPT", system)
	dir := t.TempDir()
	store := memory.NewStore(filepath.Join(dir, "brain.json"))
	catalog := memory.NewCatalogStore(filepath.Join(dir, "catalog.json"))
	groups := memory.NewGroupManager(dir, nil)
	provider := &mockTraceProvider{}
	engine := &Engine{
		Provider:       provider,
		DialoguePrompt: dialogue,
		MemoryReader:   memory.NewReader(store, 5),
		MemoryCatalog:  catalog,
		MemoryGateway:  memory.NewGateway(),
		GroupManager:   groups,
	}
	cases := []struct {
		owner, groupID, memberName, topic, fact string
	}{
		{"alice", "", "", "alice-topic", "alice-only-memory"},
		{"bob", "", "", "bob-topic", "bob-only-memory"},
		{"group:10001", "10001", "group-one-member", "group-one-topic", "group-one-only-memory"},
		{"group:10002", "10002", "group-two-member", "group-two-topic", "group-two-only-memory"},
	}
	for _, tc := range cases {
		entry := memory.MemoryEntry{
			ID: tc.fact, Owner: tc.owner, Content: tc.fact,
			Tags: []string{"cacheprobe"}, Source: memory.SourceManual,
		}
		topicStore := catalog
		catalogOwner := tc.owner
		if tc.groupID != "" {
			groupStore, err := groups.GetGroupStore(tc.groupID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := groupStore.ObserveMember("20001", tc.memberName, "", "member", "test"); err != nil {
				t.Fatal(err)
			}
			entry.Owner = "20001"
			if err := groupStore.Save(entry); err != nil {
				t.Fatal(err)
			}
			topicStore = groupStore.CatalogStore()
			catalogOwner = tc.groupID
		} else if err := store.Save(entry); err != nil {
			t.Fatal(err)
		}
		if err := topicStore.Replace(memory.UserMemoryCatalog{
			Owner: catalogOwner, Topics: []memory.MemoryTopic{{Name: tc.topic}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Reuse one engine across owners to detect stale or cross-scope context.
	for _, tc := range cases {
		t.Run(tc.owner, func(t *testing.T) {
			runCtx := RunContext{Owner: tc.owner, ActorUserID: "20001", Mock: true}
			if tc.groupID != "" {
				runCtx.OwnerType = memory.OwnerGroup
			}
			messages := []ChatMessage{{Role: "user", Content: "cacheprobe"}}
			result := engine.RunMessagesWithContext(messages, runCtx)
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			if len(provider.lastRequest.Messages) != 2 {
				t.Fatalf("expected one system and one user message, got %+v", provider.lastRequest.Messages)
			}
			prompt := provider.lastRequest.Messages[0].Content.(string)
			if !strings.HasPrefix(prompt, system+"\n\n"+dialogue+"\n\n当前系统时间：") {
				t.Fatalf("dynamic memory changed the stable prefix: %q", prompt)
			}
			for _, expected := range []string{tc.topic, tc.fact, tc.memberName} {
				if !strings.Contains(prompt, expected) {
					t.Errorf("request missing scoped context %q: %q", expected, prompt)
				}
			}
			for _, other := range cases {
				if other.owner == tc.owner {
					continue
				}
				for _, forbidden := range []string{other.topic, other.fact, other.memberName} {
					if forbidden != "" && strings.Contains(prompt, forbidden) {
						t.Errorf("request leaked context from %s: %q", other.owner, forbidden)
					}
				}
			}
			if !reflect.DeepEqual(messages, []ChatMessage{{Role: "user", Content: "cacheprobe"}}) {
				t.Errorf("request assembly mutated input history: %+v", messages)
			}
		})
	}

	// Change both the catalog and the recalled memory for an existing owner.
	if err := catalog.Replace(memory.UserMemoryCatalog{
		Owner: "alice", Topics: []memory.MemoryTopic{{Name: "updated-topic"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(memory.MemoryEntry{
		ID: "updated-memory", Owner: "alice", Content: "updated-memory",
		Tags: []string{"updatedprobe"}, Source: memory.SourceManual,
	}); err != nil {
		t.Fatal(err)
	}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "updatedprobe"}}, RunContext{Owner: "alice", Mock: true})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	prompt := provider.lastRequest.Messages[0].Content.(string)
	if !strings.HasPrefix(prompt, system+"\n\n"+dialogue+"\n\n当前系统时间：") ||
		!strings.Contains(prompt, "updated-topic") || !strings.Contains(prompt, "updated-memory") ||
		strings.Contains(prompt, "alice-topic") || strings.Contains(prompt, "alice-only-memory") {
		t.Fatalf("updated memory must follow the same stable prefix without stale context: %q", prompt)
	}
}

func TestEngineContextPromptSecurityNoticeRequestLocal(t *testing.T) {
	t.Setenv("SYSTEM_PROMPT", "固定系统设定")
	provider := &mockTraceProvider{}
	engine := &Engine{Provider: provider}
	messages := []ChatMessage{{Role: "user", Content: "用户问题"}}
	for _, notice := range []string{security.MediumRiskWarningNotice, ""} {
		result := engine.RunMessagesWithContext(messages, RunContext{SecurityNotice: notice})
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		request := provider.lastRequest.Messages
		if !strings.HasPrefix(request[0].Content.(string), "固定系统设定\n\n当前系统时间：") {
			t.Fatalf("security notice changed the stable prompt prefix: %+v", request)
		}
		if notice != "" {
			if len(request) != 3 || request[1].Role != "system" || request[1].Content != notice {
				t.Fatalf("notice must remain the second system message: %+v", request)
			}
		} else if len(request) != 2 {
			t.Fatalf("notice leaked into a subsequent ordinary request: %+v", request)
		}
		if !reflect.DeepEqual(messages, []ChatMessage{{Role: "user", Content: "用户问题"}}) {
			t.Fatalf("notice must not pollute input history: %+v", messages)
		}
	}
}
