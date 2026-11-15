package llm

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
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

func TestPersistentSessionsContinueAfterMemoryDatabaseFailure(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic','Test','now')`); err != nil {
		t.Fatal(err)
	}
	private := memory.NewSQLStore(db, "synthetic")
	groups := memory.NewSQLGroupManager(db, "synthetic", nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, group := range []bool{false, true} {
		scope := runtimescope.New(nil, nil, nil)
		sm := NewSessionManager(scope)
		if err := sm.EnablePersistence(sessionstate.New(nil, "synthetic", t.TempDir(), nil)); err != nil {
			t.Fatal(err)
		}
		provider := &mockTraceProvider{}
		e := &Engine{Scope: scope, SessionManager: sm, Provider: provider, MemoryReader: memory.NewReader(private, 10), MemoryGateway: memory.NewGateway(), GroupManager: groups}
		run := RunContext{Owner: "synthetic-user", SessionID: "private:synthetic-user"}
		if group {
			run.Owner = "group:synthetic-group"
			run.SessionID = run.Owner
			run.RouteScope = modelrouter.Scope{Platform: "telegram", GroupID: "synthetic-group"}
		}
		sm.GetOrCreate(run.SessionID)
		result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "continue"}}, run)
		if result.Error != nil || len(provider.lastRequest.Messages) == 0 {
			t.Fatalf("chat did not continue: %v", result.Error)
		}
		if scope.Context().Err() != nil {
			t.Fatal("optional memory failure paused instance")
		}
		if request, _ := sm.GetOrCreate(run.SessionID).LastActualRequest(); request == "" {
			t.Fatal("outage lost inspection snapshot")
		}
		scope.Cancel()
		scope.Wait()
	}
}

func TestAwarenessDiscoveryOutageDoesNotPauseDurableRuntime(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	groups := memory.NewSQLGroupManager(db, "synthetic", nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, durable := range []bool{false, true} {
		scope := runtimescope.New(nil, nil, nil)
		t.Cleanup(func() { scope.Cancel(); scope.Wait() })
		sessions := NewSessionManager(scope)
		if durable {
			if err := sessions.EnablePersistence(sessionstate.New(nil, "synthetic", t.TempDir(), nil)); err != nil {
				t.Fatal(err)
			}
		}
		engine := &Engine{Scope: scope, SessionManager: sessions, GroupManager: groups}
		service := &AwarenessService{Engine: engine}
		workerContext, cancelWorker := context.WithCancel(scope.Context())
		cancelWorker()
		service.Run(workerContext)
		if (scope.Context().Err() == nil) != durable {
			t.Fatalf("durable=%v runtime error=%v", durable, scope.Context().Err())
		}
	}
}
