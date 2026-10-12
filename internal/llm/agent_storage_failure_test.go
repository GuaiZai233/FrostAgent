package llm

import (
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestMemoryDatabaseFailureStopsAgentBeforeProvider(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "memory.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	private := memory.NewSQLStore(db, "test-instance")
	groups := memory.NewSQLGroupManager(db, "test-instance", nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		ctx  RunContext
	}{
		{name: "private", ctx: RunContext{Owner: "test-user"}},
		{name: "group", ctx: RunContext{Owner: "group:test-group", RouteScope: modelrouter.Scope{Platform: "telegram", GroupID: "test-group"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &mockTraceProvider{}
			engine := &Engine{Provider: provider, MemoryReader: memory.NewReader(private, 10),
				MemoryGateway: memory.NewGateway(), GroupManager: groups}
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, tc.ctx)
			if result.Error == nil {
				t.Fatal("agent continued after memory database failure")
			}
			if provider.lastRequest.Messages != nil {
				t.Fatal("provider was called without persisted memory context")
			}
		})
	}
}

func TestFailedSQLGroupPurgePausesRuntime(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "memory.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	groups := memory.NewSQLGroupManager(db, "test-instance", nil)
	store, err := groups.GetGroupStoreForPlatform("qq", "test-group")
	if err != nil {
		t.Fatal(err)
	}
	session := &SessionContext{}
	session.SetGroupStore(store)
	scope := runtimescope.New(nil, nil, nil)
	engine := &Engine{Scope: scope}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	engine.DropRejectedGroupMessage(session, nil, "message-1", "synthetic-user")
	if _, admitted := scope.Enter(); admitted {
		t.Fatal("runtime admitted new work after failed SQL memory purge")
	}
}
