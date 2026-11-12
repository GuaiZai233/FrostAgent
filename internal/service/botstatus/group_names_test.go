package botstatus

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
)

func TestGroupNamesSurviveBackendRecreationWithoutCompaction(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewGroupManager(dir, nil)
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
		engine := &llm.Engine{SessionManager: llm.NewSessionManager(), GroupManager: memory.NewGroupManager(dir, nil)}
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
}
