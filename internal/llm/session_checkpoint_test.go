package llm

import (
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
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
	if next.CountVisible() != 1 || len(next.sessions) != 0 {
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
