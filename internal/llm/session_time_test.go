package llm

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
)

func TestPrivateMessageTimeAdmissionAndConfirmedReply(t *testing.T) {
	s := NewSessionManager().GetOrCreate("private:fixture-time")
	before := time.Now().Round(0)
	s.PrepareTurnMessages(nil, "durable input", "request input", nil)
	log := NewPublicReplyLog(s, s.Epoch())
	log.RecordFinalProgress("confirmed prefix", nil)
	initial := s.Snapshot()
	log.RecordFinalProgress("confirmed reply", nil)
	log.CommitFinal("confirmed reply", false)
	history := s.Snapshot()
	if len(history) != 2 {
		t.Fatalf("unexpected history: %+v", history)
	}
	for _, message := range history {
		if message.RecordedAt.Before(before) || message.RecordedAt.After(time.Now()) {
			t.Fatalf("message time does not reflect recording: %+v", message)
		}
	}
	if !history[1].RecordedAt.Equal(initial[1].RecordedAt) {
		t.Fatal("confirmed reply update changed its first recording time")
	}
	view, _ := s.SnapshotPrivateContext(50)
	for i, message := range view.RecentStructuredMessages {
		if message.Time != FormatMessageTime(history[i].RecordedAt) {
			t.Fatalf("inspection dropped message time: %+v", message)
		}
	}
	for _, request := range []any{ChatRequest{Messages: history}, convertToCoreMessages(history)} {
		data, err := json.Marshal(request)
		if err != nil || strings.Contains(string(data), "RecordedAt") || strings.Contains(string(data), "recorded_at") {
			t.Fatalf("inspection metadata entered model wire format: %s, %v", data, err)
		}
	}
	request, _, _ := s.PrepareTurnMessages(nil, "next input", "next request", nil)
	for _, message := range request {
		if !message.RecordedAt.IsZero() {
			t.Fatal("inspection metadata entered model transcript")
		}
	}
}

func TestPrivateMessageTimeCompactAndRestart(t *testing.T) {
	dir := t.TempDir()
	s := persistentManager(t, dir).GetOrCreate("private:fixture-time-restart")
	stamp := time.Date(2026, 11, 17, 20, 34, 34, 0, time.FixedZone("fixture", 8*60*60))
	s.ReplaceMessages([]ChatMessage{
		{Role: "user", Content: "old input", RecordedAt: stamp},
		{Role: "assistant", Content: "old reply", RecordedAt: stamp.Add(6 * time.Second)},
	})
	snapshot, err := s.SnapshotPrivateCompact()
	if err != nil || !s.CommitPrivateCompact(snapshot, "fixture summary") {
		t.Fatal("compact failed", err)
	}
	s.PrepareTurnMessages(nil, "recent input", "recent input", nil)
	before, _ := s.SnapshotPrivateContext(50)
	recorded := s.Snapshot()[2].RecordedAt
	restored := persistentManager(t, dir).GetOrCreate(s.ID())
	after, _ := restored.SnapshotPrivateContext(50)
	if !restored.Snapshot()[2].RecordedAt.Equal(recorded) {
		t.Fatal("restart changed the recorded instant")
	}
	if len(after.SummaryGroups) != 1 || len(after.RecentStructuredMessages) != 1 ||
		after.SummaryGroups[0].StructuredMsgs[0].Time != FormatMessageTime(stamp) ||
		after.SummaryGroups[0].StructuredMsgs[1].Time != FormatMessageTime(stamp.Add(6*time.Second)) ||
		after.RecentStructuredMessages[0].Time == "" ||
		after.RecentStructuredMessages[0].Time != before.RecentStructuredMessages[0].Time {
		t.Fatalf("timestamps lost across compact/restart: %+v", after)
	}
}

func TestPrivateMessageTimeLegacyCheckpoint(t *testing.T) {
	snapshot := SessionCheckpoint{Version: 1, ConversationID: "private:fixture-legacy-time", History: []ChatMessage{
		{Role: "user", Content: "legacy input"}, {Role: "assistant", Content: "legacy reply"},
	}}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	record := sessionstate.Record{SessionID: snapshot.ConversationID, Revision: 1, Payload: data}
	restored, err := NewSessionManager().restoreCheckpoint(record)
	if err != nil {
		t.Fatal("legacy checkpoint rejected", err)
	}
	view, _ := restored.SnapshotPrivateContext(50)
	for _, message := range view.RecentStructuredMessages {
		if message.Time != "" {
			t.Fatal("legacy timestamp fabricated")
		}
	}
	snapshot.HistoryRecordedAt = []time.Time{time.Now()}
	record.Payload, _ = json.Marshal(snapshot)
	if ValidateCheckpoint(record) == nil {
		t.Fatal("misaligned timestamp metadata accepted")
	}
	if _, err := NewSessionManager().restoreCheckpoint(record); err == nil {
		t.Fatal("misaligned timestamp metadata restored")
	}
}
