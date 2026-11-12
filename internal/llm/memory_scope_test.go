package llm

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestMemoryReadScopesTrustedSender(t *testing.T) {
	group := memory.SearchScope{Type: memory.ScopeGroup, Platform: "qq", GroupID: "synthetic-group"}
	for _, tc := range []struct {
		name string
		run  RunContext
		want []memory.SearchScope
	}{
		{"private", RunContext{Owner: "synthetic-a"}, []memory.SearchScope{{Type: memory.ScopePrivate, Owner: "synthetic-a"}}},
		{"group sender", RunContext{ActorUserID: "synthetic-a", ActorPlatform: "aiocqhttp"}, []memory.SearchScope{group, {Type: memory.ScopePrivate, Owner: "synthetic-a"}}},
		{"next sender", RunContext{ActorUserID: "synthetic-b", ActorPlatform: "onebot"}, []memory.SearchScope{group, {Type: memory.ScopePrivate, Owner: "synthetic-b"}}},
		{"no sender", RunContext{}, []memory.SearchScope{group}},
		{"no platform", RunContext{ActorUserID: "synthetic-a"}, []memory.SearchScope{group}},
		{"platform mismatch", RunContext{ActorUserID: "synthetic-a", ActorPlatform: "telegram"}, []memory.SearchScope{group}},
		{"timer", RunContext{ActorUserID: "synthetic-a", ActorPlatform: "qq", Awareness: true}, []memory.SearchScope{group}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "private" {
				tc.run.Owner, tc.run.OwnerType = "group:synthetic-group", memory.OwnerGroup
				tc.run.RouteScope = modelrouter.Scope{Platform: "qq", GroupID: "synthetic-group"}
			}
			if got := tc.run.MemoryReadScopes(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("scopes = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestGroupPromptRecallsOnlyCurrentSenderScope(t *testing.T) {
	root := t.TempDir()
	store := memory.NewStore(filepath.Join(root, "brain.json"))
	for _, owner := range []string{"synthetic-a", "synthetic-b", "telegram:user:synthetic-a"} {
		if err := store.Save(memory.MemoryEntry{ID: owner, Owner: owner, Content: "needle-" + owner}); err != nil {
			t.Fatal(err)
		}
	}
	groups := memory.NewGroupManager(root, nil)
	for _, id := range []string{"synthetic-group", "other-group"} {
		group, err := groups.GetGroupStoreForPlatform("qq", id)
		if err != nil {
			t.Fatal(err)
		}
		if err := group.Save(memory.MemoryEntry{ID: id, Owner: "synthetic-b", Content: "needle-" + id, Source: memory.SourceManual}); err != nil {
			t.Fatal(err)
		}
	}
	provider := &mockTraceProvider{}
	engine := &Engine{Provider: provider, MemoryReader: memory.NewReader(store, 20), MemoryGateway: memory.NewGateway(), GroupManager: groups}
	for _, actor := range []string{"synthetic-a", "synthetic-b", ""} {
		run := RunContext{Owner: "group:synthetic-group", OwnerType: memory.OwnerGroup, ActorUserID: actor, ActorPlatform: "qq", RouteScope: modelrouter.Scope{Platform: "qq", GroupID: "synthetic-group"}}
		result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "needle"}}, run)
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		prompt := provider.lastRequest.Messages[0].Content.(string)
		if !strings.Contains(prompt, "needle-synthetic-group") {
			t.Fatal("missing current group")
		}
		for _, candidate := range []string{"synthetic-a", "synthetic-b", "telegram:user:synthetic-a", "other-group"} {
			if strings.Contains(prompt, "needle-"+candidate) != (candidate == actor) {
				t.Fatalf("sender %q received wrong scope %q", actor, candidate)
			}
		}
	}
}
