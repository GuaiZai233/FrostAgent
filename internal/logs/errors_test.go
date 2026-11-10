package logs

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorUUIDCorrelatesReplyStoreAndStream(t *testing.T) {
	store := New("synthetic-instance", "test", 2)
	subID, stream := store.Subscribe(nil)
	defer store.Unsubscribe(subID)
	raw := "Post https://endpoint.invalid/private: synthetic-secret"
	reply := store.ReportError(LLM_RESPONSE, "LLM 响应失败：网络请求失败", errors.New(raw), "synthetic-trace")
	entry := <-stream
	id, valid := NormalizeErrorUUID(entry.ErrorUUID)
	if !valid || !strings.Contains(reply, id) || strings.Contains(reply, raw) || strings.Contains(reply, "endpoint.invalid") {
		t.Fatalf("unsafe or uncorrelated reply: %s", reply)
	}
	if want := "[FrostAgent 错误] LLM 响应失败：网络请求失败 (Ref_ID: " + id + ")"; reply != want {
		t.Fatalf("unexpected reply format: %q", reply)
	}
	found, ok := store.FindError(strings.ToUpper(id))
	if !ok || found.Content != entry.Content || !strings.Contains(found.Content, raw) || found.TraceID != "synthetic-trace" {
		t.Fatalf("lost diagnostics: %+v", found)
	}
	other := New("other", "other", 2)
	if _, ok := other.FindError(id); ok {
		t.Fatal("lookup crossed stores")
	}
	second := store.Error(SYSTEM, "second")
	if second == id {
		t.Fatal("reused error UUID")
	}
	store.Info(SYSTEM, "evict first")
	if _, ok := store.FindError(id); ok {
		t.Fatal("evicted error still queryable")
	}
	store.Clear()
	if _, ok := store.FindError(second); ok {
		t.Fatal("cleared error still queryable")
	}
}

func TestReportErrorWithoutUnderlyingError(t *testing.T) {
	store := New("synthetic-instance", "test", 2)
	reply := store.ReportError(SYSTEM, "消息处理失败：输入过长", nil)
	_, suffix, _ := strings.Cut(reply, " (Ref_ID: ")
	entry, found := store.FindError(strings.TrimSuffix(suffix, ")"))
	if !found || entry.Content != "消息处理失败：输入过长" {
		t.Fatalf("missing fixed-error diagnostics: %+v", entry)
	}
}

func TestNormalizeErrorUUIDRejectsOtherFormats(t *testing.T) {
	for _, value := range []string{
		"", "not-a-uuid", "00000000-0000-0000-0000-000000000000",
		"00000000-0000-1000-8000-000000000000", "00000000-0000-4000-0000-000000000000",
		"00000000000040008000000000000000", "{00000000-0000-4000-8000-000000000000}",
	} {
		if _, ok := NormalizeErrorUUID(value); ok {
			t.Fatalf("accepted %q", value)
		}
	}
}
