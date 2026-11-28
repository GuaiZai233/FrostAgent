package logs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeMessageRefID(t *testing.T) {
	id := NewMessageRefID()
	if got, ok := NormalizeMessageRefID(" " + strings.ToUpper(id[:len(MessageRefPrefix)]) + id[len(MessageRefPrefix):] + " "); ok {
		t.Fatalf("prefix must be exact, got %q", got)
	}
	if got, ok := NormalizeMessageRefID("  " + id + "\n"); !ok || got != id {
		t.Fatalf("canonical id rejected: %q %v", got, ok)
	}
	for _, bad := range []string{"", "fa_msgid_", "fa_msgid_not-a-uuid", strings.TrimPrefix(id, MessageRefPrefix), "fa_error_" + strings.TrimPrefix(id, MessageRefPrefix)} {
		if _, ok := NormalizeMessageRefID(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestMessageRefRecordAndLookup(t *testing.T) {
	store := New("synthetic-instance", "test", 50)
	store.LLMResponse("first response", "group:1")
	llmID := store.LatestEntryID(LLM_RESPONSE, "group:1")
	store.logEntry(INFO, TOOL, "call", "group:1", "INTERNAL", nil, "...", "")
	toolID := store.LatestEntryID(TOOL, "group:1")
	store.LLMResponse("other session", "group:2")
	if llmID == 0 || toolID == 0 || store.LatestEntryID(LLM_RESPONSE, "group:1") != llmID || store.LatestEntryID(TOOL, "group:2") != 0 {
		t.Fatalf("latest entry lookup is not scoped by category and trace: %d %d", llmID, toolID)
	}

	id := store.RecordMessageRef(MessageRef{Platform: "onebot", SessionID: "group:1", MessageID: "42", Action: "send_group_msg",
		LLMResponseLogID: llmID, ToolLogID: toolID, Message: json.RawMessage(`[{"type":"text","data":{"text":"synthetic reply"}}]`)})
	if _, ok := NormalizeMessageRefID(id); !ok {
		t.Fatalf("invalid ref id %q", id)
	}
	entry, ok := store.FindMessageRef(id)
	if !ok || entry.MessageRefID != id || entry.TraceID != "group:1" || entry.Direction != "OUTBOUND" {
		t.Fatalf("lookup failed: %+v", entry)
	}
	var ref MessageRef
	if err := json.Unmarshal([]byte(entry.Content), &ref); err != nil || ref.RefID != id || ref.MessageID != "42" ||
		ref.LLMResponseLogID != llmID || ref.ToolLogID != toolID || !strings.Contains(string(ref.Message), "synthetic reply") {
		t.Fatalf("record lost data: %+v %v", ref, err)
	}
	if got, ok := store.FindMessageRefByMessageID("group:1", "42"); !ok || got != id {
		t.Fatalf("lookup by platform id = %q %v", got, ok)
	}
	if _, ok := store.FindMessageRefByMessageID("group:2", "42"); ok {
		t.Fatal("platform id lookup crossed sessions")
	}
	if _, ok := New("other", "other", 5).FindMessageRef(id); ok {
		t.Fatal("lookup crossed stores")
	}
	newer := store.RecordMessageRef(MessageRef{Platform: "onebot", SessionID: "group:1", MessageID: "42", Action: "send_group_msg"})
	if got, _ := store.FindMessageRefByMessageID("group:1", "42"); got != newer {
		t.Fatalf("newest record must win: %q vs %q", got, newer)
	}
	if store.RecordMessageRef(MessageRef{SessionID: "group:1"}) != "" || store.RecordMessageRef(MessageRef{MessageID: "1"}) != "" {
		t.Fatal("incomplete record was retained")
	}
	var nilStore *Store
	if nilStore.RecordMessageRef(MessageRef{SessionID: "s", MessageID: "1"}) != "" || nilStore.LatestEntryID(TOOL, "s") != 0 {
		t.Fatal("nil store must be inert")
	}
}

func TestMessageRefSurvivesPersistence(t *testing.T) {
	dir := t.TempDir()
	store := New("synthetic-instance", "test", 10)
	if err := store.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	id := store.RecordMessageRef(MessageRef{Platform: "onebot", SessionID: "private:1", MessageID: "7", Action: "send_private_msg"})
	store.StopPersistence()

	reopened := New("synthetic-instance", "test", 10)
	if err := reopened.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	defer reopened.StopPersistence()
	if entry, ok := reopened.FindMessageRef(id); !ok || entry.MessageRefID != id {
		t.Fatalf("ref lost after restart: %+v %v", entry, ok)
	}
}
