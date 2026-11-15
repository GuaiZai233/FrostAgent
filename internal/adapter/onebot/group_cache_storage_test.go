package onebot

import (
	"context"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestGroupNameStorageOutageDoesNotPauseDurableRuntime(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic','Test','now')`); err != nil {
		t.Fatal(err)
	}
	groups := memory.NewSQLGroupManager(db, "synthetic", nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, durable := range []bool{false, true} {
		scope := runtimescope.New(nil, nil, nil)
		t.Cleanup(func() { scope.Cancel(); scope.Wait() })
		sessions := llm.NewSessionManager(scope)
		if durable {
			if err := sessions.EnablePersistence(sessionstate.New(nil, "synthetic", t.TempDir(), nil)); err != nil {
				t.Fatal(err)
			}
		}
		engine := &llm.Engine{Scope: scope, SessionManager: sessions, GroupManager: groups}
		connection := newWSConnection(nil, scope)
		defer connection.cancel()
		connection.engine = engine
		// Protocol fixture, not an actual group ID.
		connection.pendingGroupByEcho["synthetic-echo"] = pendingGroupInfo{GroupID: 7, RequestedAt: time.Now()}
		connection.pendingGroupByID[7] = "synthetic-echo"
		if !connection.handleAPIResponse([]byte(`{"echo":"synthetic-echo","status":"ok","retcode":0,"data":{"group_id":7,"group_name":"Synthetic den"}}`)) {
			t.Fatal("group response ignored")
		}
		if (scope.Context().Err() == nil) != durable {
			t.Fatalf("durable=%v runtime error=%v", durable, scope.Context().Err())
		}
		if connection.cachedGroupName(7) != "Synthetic den" {
			t.Fatal("SQL outage erased the connection cache")
		}
	}
}
