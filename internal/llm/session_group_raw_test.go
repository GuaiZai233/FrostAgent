package llm

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSnapshotGroupContext_Limit(t *testing.T) {
	s := &SessionContext{}
	for i := 1; i <= 10; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("User%d: message %d", i, i), 20, fmt.Sprintf("msg_%d", i))
	}

	snap := s.SnapshotGroupContext(5, 10000, "")
	if len(snap.RecentMessages) != 5 {
		t.Fatalf("expected 5 recent messages, got %d", len(snap.RecentMessages))
	}

	// Should keep newest 5 messages (6 to 10)
	if got := snap.RecentMessages[0]; got != "User6: message 6" {
		t.Errorf("expected first kept message User6: message 6, got %q", got)
	}
	if got := snap.RecentMessages[4]; got != "User10: message 10" {
		t.Errorf("expected last kept message User10: message 10, got %q", got)
	}
}

func TestSnapshotGroupContext_MaxChars(t *testing.T) {
	s := &SessionContext{}
	msg1 := "User1: " + strings.Repeat("A", 100)
	msg2 := "User2: " + strings.Repeat("B", 100)
	msg3 := "User3: " + strings.Repeat("C", 100)

	s.AppendGroupCompactMessage(msg1, 20, "id_1")
	s.AppendGroupCompactMessage(msg2, 20, "id_2")
	s.AppendGroupCompactMessage(msg3, 20, "id_3")

	// Char budget only allows msg2 and msg3
	// msg3 is ~107 chars, msg2 is ~107 chars, msg1 is ~107 chars.
	// maxChars = 220 -> should keep msg2 and msg3, dropping msg1
	snap := s.SnapshotGroupContext(10, 220, "")
	if len(snap.RecentMessages) != 2 {
		t.Fatalf("expected 2 messages within char budget, got %d: %v", len(snap.RecentMessages), snap.RecentMessages)
	}
	if snap.RecentMessages[0] != msg2 {
		t.Errorf("expected msg2, got %q", snap.RecentMessages[0])
	}
	if snap.RecentMessages[1] != msg3 {
		t.Errorf("expected msg3, got %q", snap.RecentMessages[1])
	}
}

func TestSnapshotGroupContext_SingleMessageExceedsMaxChars(t *testing.T) {
	s := &SessionContext{}
	longMsg := "User: " + strings.Repeat("X", 500)
	s.AppendGroupCompactMessage(longMsg, 20, "id_long")

	snap := s.SnapshotGroupContext(10, 50, "")
	if len(snap.RecentMessages) != 1 {
		t.Fatalf("expected 1 truncated message, got %d", len(snap.RecentMessages))
	}
	if len([]rune(snap.RecentMessages[0])) != 50 {
		t.Errorf("expected length 50 runes, got %d", len([]rune(snap.RecentMessages[0])))
	}
}

func TestSnapshotGroupContext_ExcludeMessageID(t *testing.T) {
	s := &SessionContext{}
	s.AppendGroupCompactMessage("User1: hello", 20, "id_1")
	s.AppendGroupCompactMessage("User2: trigger @bot", 20, "id_trigger")
	s.AppendGroupCompactMessage("User3: follow up", 20, "id_3")

	snap := s.SnapshotGroupContext(10, 10000, "id_trigger")
	if len(snap.RecentMessages) != 2 {
		t.Fatalf("expected 2 messages after excluding trigger messageID, got %d", len(snap.RecentMessages))
	}
	if snap.RecentMessages[0] != "User1: hello" {
		t.Errorf("expected User1: hello, got %q", snap.RecentMessages[0])
	}
	if snap.RecentMessages[1] != "User3: follow up" {
		t.Errorf("expected User3: follow up, got %q", snap.RecentMessages[1])
	}
}

func TestSnapshotGroupContext_Atomic(t *testing.T) {
	s := &SessionContext{}
	s.SetGroupRunningSummary("initial summary")

	var wg sync.WaitGroup
	workers := 10
	iterations := 100

	wg.Add(workers * 2)

	// Writer goroutines: appending messages and updating summary
	for w := range workers {
		go func(workerID int) {
			defer wg.Done()
			for i := range iterations {
				s.AppendGroupCompactMessage(
					fmt.Sprintf("Worker%d: text %d", workerID, i),
					20,
					fmt.Sprintf("w%d_msg_%d", workerID, i),
				)
				if i%10 == 0 {
					s.SetGroupRunningSummary(fmt.Sprintf("summary from worker %d step %d", workerID, i))
				}
			}
		}(w)
	}

	// Reader goroutines: taking atomic snapshots
	for r := range workers {
		go func(readerID int) {
			defer wg.Done()
			for i := range iterations {
				snap := s.SnapshotGroupContext(10, 5000, fmt.Sprintf("w0_msg_%d", i))
				_ = snap.RunningSummary
				_ = len(snap.RecentMessages)
			}
		}(r)
	}

	wg.Wait()

	finalSnap := s.SnapshotGroupContext(20, 10000, "")
	if finalSnap.RunningSummary == "" {
		t.Errorf("expected non-empty running summary")
	}
	if len(finalSnap.RecentMessages) == 0 {
		t.Errorf("expected recent messages to be present")
	}
}

func TestSnapshotGroupContext_RingBufferOverflow(t *testing.T) {
	s := &SessionContext{}
	bufferSize := 5
	for i := 1; i <= 15; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg_%d", i), bufferSize, fmt.Sprintf("id_%d", i))
	}

	snap := s.SnapshotGroupContext(10, 10000, "")
	if len(snap.RecentMessages) != bufferSize {
		t.Fatalf("expected %d messages in ring buffer, got %d", bufferSize, len(snap.RecentMessages))
	}

	// Messages should be msg_11 to msg_15
	for i, expected := range []string{"msg_11", "msg_12", "msg_13", "msg_14", "msg_15"} {
		if snap.RecentMessages[i] != expected {
			t.Errorf("at index %d: expected %q, got %q", i, expected, snap.RecentMessages[i])
		}
	}
}

func TestSnapshotGroupContext_SummaryGroups(t *testing.T) {
	s := &SessionContext{}
	s.AppendGroupCompactMessage("UserA: 路线定了吗？", 20, "msg_1")
	s.AppendGroupCompactMessage("UserB: 推荐走龙脊线", 20, "msg_2")
	s.AppendGroupCompactMessage("UserA: 周六上午9点集合", 20, "msg_3")

	compactSnap, ready := s.SnapshotGroupCompact(3)
	if !ready {
		t.Fatalf("expected compact snapshot to be ready")
	}
	if len(compactSnap.MessageIDs) != 3 || compactSnap.MessageIDs[0] != "msg_1" || compactSnap.MessageIDs[2] != "msg_3" {
		t.Fatalf("unexpected message IDs in snapshot: %v", compactSnap.MessageIDs)
	}

	summaryText := "群里确认路线为龙脊线，集合时间为周六上午9点。"
	ok := s.CommitGroupCompact(compactSnap, summaryText)
	if !ok {
		t.Fatalf("expected CommitGroupCompact to succeed")
	}

	// Now append a new uncompacted message
	s.AppendGroupCompactMessage("UserC: 我也去！", 20, "msg_4")

	snap := s.SnapshotGroupContext(10, 10000, "")
	if snap.RunningSummary != summaryText {
		t.Errorf("expected running summary %q, got %q", summaryText, snap.RunningSummary)
	}
	if len(snap.RecentMessages) != 1 || snap.RecentMessages[0] != "UserC: 我也去！" {
		t.Errorf("expected 1 recent uncompacted message, got %v", snap.RecentMessages)
	}
	if len(snap.SummaryGroups) != 1 {
		t.Fatalf("expected 1 summary group, got %d", len(snap.SummaryGroups))
	}
	group := snap.SummaryGroups[0]
	if group.Summary != summaryText {
		t.Errorf("expected group summary %q, got %q", summaryText, group.Summary)
	}
	if len(group.MessageIDs) != 3 || group.StartMessageID != "msg_1" || group.EndMessageID != "msg_3" {
		t.Errorf("unexpected summary group metadata: %+v", group)
	}

	// A later compact replaces the inspected source batch while preserving the
	// cumulative summary produced after that batch.
	s.AppendGroupCompactMessage("UserD: 记得带水", 20, "msg_5")
	latestCompactSnap, latestReady := s.SnapshotGroupCompact(2)
	if !latestReady {
		t.Fatalf("expected latest compact snapshot to be ready")
	}
	updatedSummary := "群里确认路线为龙脊线，集合时间为周六上午9点；UserC 和 UserD 也会参加。"
	if !s.CommitGroupCompact(latestCompactSnap, updatedSummary) {
		t.Fatalf("expected latest CommitGroupCompact to succeed")
	}

	latest := s.SnapshotGroupContext(10, 10000, "")
	if latest.RunningSummary != updatedSummary {
		t.Errorf("expected updated running summary %q, got %q", updatedSummary, latest.RunningSummary)
	}
	if len(latest.RecentMessages) != 0 {
		t.Errorf("expected no uncompacted messages, got %v", latest.RecentMessages)
	}
	if len(latest.SummaryGroups) != 1 {
		t.Fatalf("expected exactly 1 latest summary group, got %d", len(latest.SummaryGroups))
	}
	latestGroup := latest.SummaryGroups[0]
	if latestGroup.Summary != updatedSummary {
		t.Errorf("expected latest group summary %q, got %q", updatedSummary, latestGroup.Summary)
	}
	if len(latestGroup.MessageIDs) != 2 || latestGroup.StartMessageID != "msg_4" || latestGroup.EndMessageID != "msg_5" {
		t.Errorf("unexpected latest summary group metadata: %+v", latestGroup)
	}
}

