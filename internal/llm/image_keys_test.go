package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

const (
	syntheticKeyA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	syntheticKeyB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestCheckpointRestartKeepsPrivateAndGroupImageKeys(t *testing.T) {
	dir := t.TempDir()
	sm := persistentManager(t, dir)

	private := sm.GetOrCreate("private:synthetic")
	private.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "User Message: [图片]"})
	private.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "[image sticker.png]"})
	private.SetHistoryImageKeys(private.historySeq[0], []string{syntheticKeyA})
	private.SetHistoryImageKeys(private.historySeq[1], []string{syntheticKeyB})

	group := sm.GetOrCreate("group:synthetic")
	group.AppendGroupCompactMessage(GroupCompactMessage{Role: "user", Sender: "synthetic", Content: "[图片]", ImageKeys: []string{syntheticKeyA}}, 0)

	next := persistentManager(t, dir)
	_, history := next.GetOrCreate("private:synthetic").SnapshotPrivateContext(50)
	if len(history) != 2 || !reflect.DeepEqual(history[0].ImageKeys, []string{syntheticKeyA}) || !reflect.DeepEqual(history[1].ImageKeys, []string{syntheticKeyB}) {
		t.Fatalf("private history keys lost across restart: %+v", history)
	}
	snapshot := next.GetOrCreate("group:synthetic").SnapshotGroupContext(50, 100000, "")
	if len(snapshot.RecentStructuredMessages) != 1 || !reflect.DeepEqual(snapshot.RecentStructuredMessages[0].ImageKeys, []string{syntheticKeyA}) {
		t.Fatalf("group buffer keys lost across restart: %+v", snapshot.RecentStructuredMessages)
	}
}

func TestImageKeysNeverEnterModelPayloads(t *testing.T) {
	message := ChatMessage{Role: "user", Content: "[图片]", ImageKeys: []string{syntheticKeyA}}
	data, err := json.Marshal(message)
	if err != nil || strings.Contains(string(data), syntheticKeyA) {
		t.Fatalf("chat message encoding leaked the key: %s %v", data, err)
	}
	group := FormatGroupCompactMessagesJSONL([]GroupCompactMessage{{Role: "user", Content: "[图片]", ImageKeys: []string{syntheticKeyA}}})
	if strings.Contains(group, syntheticKeyA) {
		t.Fatalf("group compact input leaked the key: %s", group)
	}
}

func TestPublicReplyLogPairsKeysWithFinalPrivateHistory(t *testing.T) {
	session := NewSessionManager().GetOrCreate("private:synthetic")
	log := NewPublicReplyLog(session, session.Epoch())
	log.RecordWithImages("hello [image a.png]", []string{syntheticKeyA}, nil)
	log.RecordWithImages("plain", nil, nil)
	log.RecordWithImages("[image b.png]", []string{syntheticKeyB}, nil)
	if !log.CommitFinal("hello [image a.png]\nplain\n[image b.png]", false) {
		t.Fatal("final commit rejected")
	}
	history := session.Snapshot()
	if len(history) != 1 || !reflect.DeepEqual(history[0].ImageKeys, []string{syntheticKeyA, syntheticKeyB}) {
		t.Fatalf("final reply keys not paired per placeholder: %+v", history)
	}

	// A rewritten final with a different placeholder count must not borrow keys.
	other := NewSessionManager().GetOrCreate("private:synthetic-other")
	rewritten := NewPublicReplyLog(other, other.Epoch())
	rewritten.RecordWithImages("[image a.png]", []string{syntheticKeyA}, nil)
	rewritten.CommitFinal("no images any more", false)
	if got := other.Snapshot(); len(got) != 1 || len(got[0].ImageKeys) != 0 {
		t.Fatalf("mismatched final reused keys: %+v", got)
	}
}

func TestAlignImageKeysPadsPlaceholdersWithoutKeys(t *testing.T) {
	if got := alignImageKeys("[图片] and [image x.png]", []string{syntheticKeyA}); !reflect.DeepEqual(got, []string{syntheticKeyA, ""}) {
		t.Fatalf("got %#v", got)
	}
	if got := alignImageKeys("no placeholders", []string{syntheticKeyA}); got != nil {
		t.Fatalf("got %#v", got)
	}
}
