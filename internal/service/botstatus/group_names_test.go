package botstatus

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestGroupNamesSurviveBackendRecreationWithoutCompaction(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			newManager := func() *memory.GroupManager { return memory.NewGroupManager(dir, nil) }
			if backend == "sqlite" {
				t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
				t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(dir, "fixture.db"))
				db, err := storage.Open(context.Background(), dir)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('fixture-instance', 'Fixture', 'now')`); err != nil {
					t.Fatal(err)
				}
				newManager = func() *memory.GroupManager { return memory.NewSQLGroupManager(db, "fixture-instance", nil) }
			}
			manager := newManager()
			for _, platform := range []string{"qq", "fixture-platform"} {
				store, err := manager.GetGroupStoreForPlatform(platform, "fixture-group")
				if err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateGroupName("Persistent den"); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"Persistent den", "Renamed den"} {
				engine := &llm.Engine{SessionManager: llm.NewSessionManager(), GroupManager: newManager()}
				service := New(engine, "fixture-version", "")
				response, err := service.GetSessions(context.Background(), connect.NewRequest(&v1.GetSessionsRequest{}))
				if err != nil {
					t.Fatal(err)
				}
				if len(response.Msg.Sessions) != 2 {
					t.Fatalf("expected two durable group identities: %+v", response.Msg.Sessions)
				}
				for _, session := range response.Msg.Sessions {
					if session.GroupName != name {
						t.Fatalf("name lost after restart: %+v", session)
					}
				}
				for _, platform := range []string{"qq", "fixture-platform"} {
					store, err := engine.GroupManager.GetGroupStoreForPlatform(platform, "fixture-group")
					if err != nil {
						t.Fatal(err)
					}
					if err := store.UpdateGroupName("Renamed den"); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
