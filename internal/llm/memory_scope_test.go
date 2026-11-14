package llm

import (
	"context"
	"os"
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
		{"group sender", RunContext{ActorUserID: "synthetic-a", ActorPlatform: "qq"}, []memory.SearchScope{group, {Type: memory.ScopePrivate, Owner: "synthetic-a"}}},
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
	catalog := memory.NewCatalogStore(filepath.Join(root, "catalog.json"))
	for _, actor := range []string{"synthetic-a", "synthetic-b"} {
		if err := catalog.Replace(memory.UserMemoryCatalog{Owner: actor, Topics: []memory.MemoryTopic{{Name: "catalog-" + actor}}}); err != nil {
			t.Fatal(err)
		}
	}
	engine := &Engine{Provider: provider, MemoryReader: memory.NewReader(store, 20), MemoryGateway: memory.NewGateway(), GroupManager: groups, MemoryCatalog: catalog}
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
		for _, candidate := range []string{"synthetic-a", "synthetic-b"} {
			if strings.Contains(prompt, "catalog-"+candidate) != (candidate == actor) {
				t.Fatalf("sender %q received wrong catalog %q", actor, candidate)
			}
		}
	}
}

func TestMemoryScopeRetrievalAndRecallAccounting(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := memory.NewStore(filepath.Join(root, "brain.json"))
	for _, entry := range []memory.MemoryEntry{
		{ID: "shared-id", Owner: "synthetic-user", Content: "needle-user", Tags: []string{"topic"}},
		{ID: "unrelated", Owner: "synthetic-other", Content: "needle-other", Tags: []string{"topic"}},
		{ID: "compact", Owner: "synthetic-user", Content: "needle-compact", Tags: []string{"topic"}, Source: memory.SourceCompact},
	} {
		if err := store.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	groups := memory.NewGroupManager(root, nil)
	group, err := groups.GetGroupStoreForPlatform("qq", "synthetic-group")
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Save(memory.MemoryEntry{ID: "shared-id", Content: "needle-group", Tags: []string{"topic"}, Source: memory.SourceManual}); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{MemoryReader: memory.NewReader(store, 1), MemoryGateway: memory.NewGateway(), GroupManager: groups}
	for _, scope := range []memory.SearchScope{
		{Type: memory.ScopePrivate, Owner: "synthetic-user"},
		{Type: memory.ScopeGroup, Platform: "qq", GroupID: "synthetic-group"},
	} {
		for _, tagsOnly := range []bool{false, true} {
			query, tags := "needle", []string(nil)
			if tagsOnly {
				query, tags = "", []string{"topic"}
			}
			entries, err := engine.SearchMemoryScope(ctx, scope, query, tags)
			if err != nil || len(entries) != 1 || entries[0].ID != "shared-id" || entries[0].ScopeType != scope.Type {
				t.Fatalf("wrong search scope: %#v %v", entries, err)
			}
		}
		entries, err := engine.ListMemoryScope(ctx, scope)
		if err != nil || len(entries) != 1 || entries[0].ID != "shared-id" || entries[0].ScopeType != scope.Type {
			t.Fatalf("wrong list scope: %#v %v", entries, err)
		}
		if err := engine.RecordMemoryScopeRecall(scope, entries); err != nil {
			t.Fatal(err)
		}
		if err := engine.RecordMemoryScopeRecall(scope, nil); err != nil {
			t.Fatal(err)
		}
	}
	users, err := store.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range users {
		want := 0
		if entry.ID == "shared-id" {
			want = 1
		}
		if entry.AccessCount != want {
			t.Fatalf("recall counted in wrong user record: %#v", entry)
		}
	}
	entries, err := group.ListAll()
	if err != nil || len(entries) != 1 || entries[0].AccessCount != 1 {
		t.Fatalf("wrong group recall: %#v %v", entries, err)
	}
}

func TestMemoryScopeUnavailableAndCorruptStorage(t *testing.T) {
	ctx := context.Background()
	user := memory.SearchScope{Type: memory.ScopePrivate, Owner: "synthetic-user"}
	group := memory.SearchScope{Type: memory.ScopeGroup, Platform: "qq", GroupID: "synthetic-group"}
	for _, engine := range []*Engine{{}, {MemoryGateway: memory.NewGateway()}} {
		for _, scope := range []memory.SearchScope{user, group} {
			if _, err := engine.SearchMemoryScope(ctx, scope, "needle", nil); err == nil {
				t.Fatal("search accepted unavailable storage")
			}
			if _, err := engine.ListMemoryScope(ctx, scope); err == nil {
				t.Fatal("list accepted unavailable storage")
			}
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "brain.json")
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{MemoryReader: memory.NewReader(memory.NewStore(path), 20), MemoryGateway: memory.NewGateway(), GroupManager: memory.NewGroupManager(root, nil)}
	for _, scope := range []memory.SearchScope{user, {Type: memory.ScopePrivate}, {Type: memory.ScopeGroup}} {
		if _, err := engine.SearchMemoryScope(ctx, scope, "needle", nil); err == nil {
			t.Fatal("search swallowed storage/scope error")
		}
		if _, err := engine.ListMemoryScope(ctx, scope); err == nil {
			t.Fatal("list swallowed storage/scope error")
		}
	}
	if err := engine.RecordMemoryScopeRecall(memory.SearchScope{Type: memory.ScopeGroup}, []memory.MemoryEntry{{ID: "shared-id"}}); err == nil {
		t.Fatal("recall accepted invalid group")
	}
	for _, run := range []RunContext{{}, {Owner: "group:"}, {Owner: "synthetic-group", OwnerType: memory.OwnerGroup}} {
		if got := run.MemoryReadScopes(); len(got) != 0 {
			t.Fatalf("incomplete identity granted memory scope: %#v", got)
		}
	}
	if got := (RunContext{Owner: "group:synthetic-group"}).MemoryReadScopes(); len(got) != 1 || got[0].Type != memory.ScopeGroup {
		t.Fatalf("legacy group route became user scope: %#v", got)
	}
}
