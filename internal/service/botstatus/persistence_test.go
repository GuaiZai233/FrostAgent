package botstatus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestOverviewCountsLiveSessionsWhileInspectionIncludesColdCheckpoints(t *testing.T) {
	dir := t.TempDir()
	scope := runtimescope.New(nil, nil, nil)
	defer func() { scope.Cancel(); scope.Wait() }()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic','Test','now')`); err != nil {
		t.Fatal(err)
	}
	manager := llm.NewSessionManager(scope)
	if err := manager.EnablePersistence(sessionstate.New(db, "synthetic", dir, nil)); err != nil {
		t.Fatal(err)
	}
	service := New(&llm.Engine{Scope: scope, SessionManager: manager}, "test", "")
	session := manager.GetOrCreate("private:synthetic")
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "stored context"})
	session.Lock()
	session.UpdatedAt = time.Now().Add(-48 * time.Hour)
	session.Unlock()
	overview, err := service.GetOverview(context.Background(), connect.NewRequest(&v1.GetOverviewRequest{}))
	if err != nil || overview.Msg.ActiveSessions != 1 {
		t.Fatalf("live overview: %v %v", overview, err)
	}
	manager.Cleanup()
	list, err := service.GetSessions(context.Background(), connect.NewRequest(&v1.GetSessionsRequest{}))
	if err != nil || len(list.Msg.Sessions) != 1 {
		t.Fatalf("cold session missing from inspection: %v %v", list, err)
	}
	overview, err = service.GetOverview(context.Background(), connect.NewRequest(&v1.GetOverviewRequest{}))
	if err != nil || overview.Msg.ActiveSessions != 0 || manager.Count() != 0 {
		t.Fatalf("cold checkpoint counted as active: %v %v", overview, err)
	}
	if err := manager.Checkpoints.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("checkpoint files: %v %v", files, err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	_, err = service.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{SessionId: "private:synthetic"}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("unrecoverable inspection returned empty context as success: %v", err)
	}
}
