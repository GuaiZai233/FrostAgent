package llm

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
)

// A turn captured before /reset must not enter the post-reset extraction batch.
func TestEnqueueExtractionTurnAtEpochRejectsStaleTurnAfterReset(t *testing.T) {
	engine := &Engine{MemoryWriter: memory.NewWriter(nil)}
	session := NewSessionManager().GetOrCreate("group:extraction-epoch")
	items := []memory.PendingExtractionItem{{Owner: "group:1", OwnerType: memory.OwnerGroup, ScopeType: memory.ScopeGroup, GroupID: "1", SpeakerID: "2", Message: core.ChatMessage{Role: core.RoleUser, Content: "stale turn"}}}

	stale := session.Epoch()
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	engine.EnqueueExtractionTurnAtEpoch(session, stale, items)
	if got := session.PendingTurnCount(); got != 0 {
		t.Fatalf("stale turn admitted after reset: pending=%d", got)
	}

	engine.EnqueueExtractionTurnAtEpoch(session, session.Epoch(), items)
	if got := session.PendingTurnCount(); got != 1 {
		t.Fatalf("current-epoch turn not admitted: pending=%d", got)
	}
}
