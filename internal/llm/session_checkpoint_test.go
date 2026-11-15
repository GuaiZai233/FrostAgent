package llm

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func persistentManager(t *testing.T, dir string) *SessionManager {
	t.Helper()
	scope := runtimescope.New(nil, nil, nil)
	t.Cleanup(func() { scope.Cancel(); scope.Wait() })
	sm := NewSessionManager(scope)
	if err := sm.EnablePersistence(sessionstate.New(nil, "synthetic", dir, nil)); err != nil {
		t.Fatal(err)
	}
	return sm
}

func TestCheckpointRestartPreservesPrivateInspectionAndMedia(t *testing.T) {
	dir := t.TempDir()
	sm := persistentManager(t, dir)
	s := sm.GetOrCreate("private:synthetic")
	s.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "original"})
	s.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "answer"})
	snap, err := s.SnapshotPrivateCompact()
	if err != nil {
		t.Fatal(err)
	}
	s.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "recent"})
	if !s.CommitPrivateCompact(snap, "summary") {
		t.Fatal("compact did not commit")
	}
	media := []core.ContentPart{{Type: "text", Text: "image"}, {Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/png;base64,c3ludGhldGlj"}}}
	s.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: media})
	s.SetLastActualRequest(s.Epoch(), "synthetic-model", core.ChatRequest{Messages: []core.ChatMessage{{Role: core.RoleUser, Content: media}}})
	s.SetLastPromptTrace("system prompt", "synthetic-model")
	s.SetLastUserPromptAtEpoch(s.Epoch(), "user prompt")
	before, history := s.SnapshotPrivateContext(50)
	next := persistentManager(t, dir)
	if next.CountVisible() != 0 || len(next.cold) != 1 || len(next.sessions) != 0 {
		t.Fatal("restore should keep cold sessions available without activating them")
	}
	restored := next.GetOrCreate("private:synthetic")
	after, restoredHistory := restored.SnapshotPrivateContext(50)
	if request, at := restored.LastActualRequest(); request == "" || at.IsZero() {
		t.Fatal("actual request attachments lost")
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(history, restoredHistory) {
		t.Fatalf("inspection changed across restart\nbefore=%+v\nafter=%+v", before, after)
	}
	if system, model := restored.LastPromptTrace(); system != "system prompt" || model != "synthetic-model" || restored.LastUserPrompt() != "user prompt" {
		t.Fatal("prompt metadata lost")
	}
	if _, ok := restored.Snapshot()[len(restored.History)-1].Content.([]core.ContentPart); !ok {
		t.Fatal("media restored with generic JSON type")
	}
	restored.mu.Lock()
	restored.UpdatedAt = time.Now().Add(-48 * time.Hour)
	restored.unlockCheckpoint()
	next.Cleanup()
	if len(next.sessions) != 0 || len(next.ListSessions(0, 10)) != 1 {
		t.Fatal("TTL deleted persisted inspection")
	}
	if err := next.ResetSession("private:synthetic"); err != nil {
		t.Fatal(err)
	}
	reset := persistentManager(t, dir).GetOrCreate("private:synthetic")
	if len(reset.Snapshot()) != 0 || reset.LastUserPrompt() != "" {
		t.Fatal("reset context resurrected")
	}
}

func TestCheckpointAdmittedGroupOnlyAndDeletion(t *testing.T) {
	dir := t.TempDir()
	sm := persistentManager(t, dir)
	s := sm.GetOrCreate("group:synthetic")
	s.AppendGroupCompactMessage("[user] Alice: admitted", 20, "admitted")
	s.StageGroupCompactMessage("[user] Eve: unreviewed", 20, "staged")
	next := persistentManager(t, dir)
	restored := next.GetOrCreate("group:synthetic")
	if messages := restored.GroupCompactBufferMessages(); len(messages) != 1 || messages[0].MessageID != "admitted" {
		t.Fatal("unadmitted group input survived restart")
	}
	next.Delete("onebot:group:synthetic")
	restored.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "stale writer"})
	last := persistentManager(t, dir)
	if last.CountVisible() != 0 || len(last.GetOrCreate("group:synthetic").GroupCompactBufferMessages()) != 0 {
		t.Fatal("deleted session resurrected")
	}
	mock := last.GetOrCreate("private:mock")
	mock.MarkMock()
	mock.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "mock input"})
	if again := persistentManager(t, dir); again.CountVisible() != 0 {
		t.Fatal("mock state persisted")
	}
}

func TestDeletedCheckpointCannotOverwriteReplacement(t *testing.T) {
	dir := t.TempDir()
	sm := persistentManager(t, dir)
	stale := sm.GetOrCreate("private:synthetic")
	stale.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "old"})
	sm.Delete("private:synthetic")
	current := sm.GetOrCreate("private:synthetic")
	current.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "new"})
	for i := 0; i < 3; i++ {
		stale.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "late callback"})
	}
	restored := persistentManager(t, dir).GetOrCreate("private:synthetic")
	if history := restored.Snapshot(); len(history) != 1 || history[0].Content != "new" {
		t.Fatal("stale deleted session overwrote its replacement")
	}
}

func persistentSQLManager(t *testing.T, db *storage.DB, dir string) *SessionManager {
	t.Helper()
	scope := runtimescope.New(nil, nil, nil)
	t.Cleanup(func() { scope.Cancel(); scope.Wait() })
	manager := NewSessionManager(scope)
	if err := manager.EnablePersistence(sessionstate.New(db, "synthetic", dir, nil)); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestColdCheckpointRecoversSQLAfterLocalDamage(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt", "delete"} {
		t.Run(damage, func(t *testing.T) {
			db, err := storage.Open(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic','Test','now')`); err != nil {
				t.Fatal(err)
			}
			dir, id := t.TempDir(), "private:synthetic"
			manager := persistentSQLManager(t, db, dir)
			session := manager.GetOrCreate(id)
			session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "recoverable history"})
			session.SetLastPromptTrace("recoverable prompt", "synthetic-model")
			if err := manager.Checkpoints.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			cold := persistentSQLManager(t, db, dir)
			path := filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
			if damage == "missing" {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte("corrupt"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if damage == "delete" {
				cold.Delete(id)
				if err := cold.Checkpoints.Flush(context.Background()); err != nil {
					t.Fatal(err)
				}
				if restored := persistentSQLManager(t, db, dir); len(restored.cold) != 0 {
					t.Fatal("delete failed to persist its tombstone after SQL recovery")
				}
				return
			}
			restored := cold.GetOrCreate(id)
			if restored.RecoveryError() != nil || len(restored.Snapshot()) != 1 || restored.Snapshot()[0].Content != "recoverable history" {
				t.Fatal("cold read failure replaced the SQL history")
			}
			if prompt, _ := restored.LastPromptTrace(); prompt != "recoverable prompt" {
				t.Fatal("SQL recovery lost prompt inspection")
			}
			restored.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "after recovery"})
			if err := cold.Checkpoints.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			if history := persistentSQLManager(t, db, dir).GetOrCreate(id).Snapshot(); len(history) != 2 || history[0].Content != "recoverable history" {
				t.Fatal("new message overwrote recoverable history")
			}
		})
	}
}

func TestUnreadableColdCheckpointFencesOnlyItsSession(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic','Test','now')`); err != nil {
		t.Fatal(err)
	}
	dir, id := t.TempDir(), "private:synthetic"
	original := persistentSQLManager(t, db, dir)
	original.GetOrCreate(id).AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "preserve me"})
	if err := original.Checkpoints.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager := persistentSQLManager(t, db, dir)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	blocked := manager.GetOrCreate(id)
	if !errors.Is(blocked.RecoveryError(), sessionstate.ErrUnavailable) || manager.CountVisible() != 0 || len(manager.cold) != 1 {
		t.Fatal("unreadable checkpoint became a new live session")
	}
	blocked.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "must not replace history"})
	turn := blocked.ReserveSequenceTurn("synthetic", false, false)
	defer turn.Done()
	if !turn.Interrupted() {
		t.Fatal("unrecoverable session admitted a turn")
	}
	provider := &mockTraceProvider{}
	engine := &Engine{Scope: manager.Scope, SessionManager: manager, Provider: provider}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "must not run"}}, RunContext{SessionID: id})
	if result.Error == nil || len(provider.lastRequest.Messages) != 0 || manager.Scope.Context().Err() != nil {
		t.Fatal("checkpoint failure ran the model or paused unrelated sessions")
	}
	manager.GetOrCreate("private:peer").AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "peer continues"})
	manager.Delete(id)
	if len(manager.cold) != 1 || manager.ResetSession(id) == nil {
		t.Fatal("failed deletion/reset silently discarded the recovery ID")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("blocked context overwrote the missing mirror")
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	restored := manager.GetOrCreate(id)
	if restored.RecoveryError() != nil || len(restored.Snapshot()) != 1 || restored.Snapshot()[0].Content != "preserve me" || manager.Checkpoints.Status().Error != "" {
		t.Fatal("recovery did not retry after the mirror was repaired")
	}
	blocked.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "stale blocked callback"})
	if history := persistentManager(t, dir).GetOrCreate(id).Snapshot(); len(history) != 1 || history[0].Content != "preserve me" {
		t.Fatal("blocked callback replaced recovered history")
	}
}
