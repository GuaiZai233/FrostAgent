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
	var reply string
	output := captureConsole(t, func() {
		reply = store.ReportError(LLM_RESPONSE, "LLM 响应失败：网络请求失败", errors.New(raw), "synthetic-trace")
	})
	entry := <-stream
	id, valid := NormalizeErrorUUID(entry.ErrorUUID)
	if !valid || !strings.Contains(reply, id) || strings.Contains(reply, raw) || strings.Contains(reply, "endpoint.invalid") {
		t.Fatalf("unsafe or uncorrelated reply: %s", reply)
	}
	if want := "[FrostAgent 错误] LLM 响应失败：网络请求失败 (Ref_ID: " + id + ")"; reply != want {
		t.Fatalf("unexpected reply format: %q", reply)
	}
	if !strings.Contains(output, "[Ref_ID: "+id+"]") || output != Console(entry)+"\n" {
		t.Fatalf("console lost the reply reference or changed on replay: %q", output)
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

func TestConsoleKeepsErrorReference(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category Category
		content  string
	}{
		{name: "empty", category: SYSTEM},
		{name: "multiline", category: TOOL, content: "first\nsecond\tthird"},
		{name: "long", category: LLM_RESPONSE, content: strings.Repeat("诊断", 3000)},
		{name: "websocket", category: WEBSOCKET, content: "connection failed"},
		{name: "http", category: HTTP, content: "request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := New("synthetic-instance", "test", 1)
			var id string
			output := captureConsole(t, func() {
				if tc.category == WEBSOCKET {
					store.Websocket(ERROR, tc.content)
				} else {
					id = store.Error(tc.category, tc.content)
				}
			})
			entry := store.Snapshot()[0]
			if _, valid := NormalizeErrorUUID(entry.ErrorUUID); !valid {
				t.Fatalf("missing error reference: %+v", entry)
			}
			if id != "" && id != entry.ErrorUUID {
				t.Fatalf("returned reference = %q, stored reference = %q", id, entry.ErrorUUID)
			}
			if entry.Content != tc.content {
				t.Fatal("console formatting changed stored diagnostics")
			}
			want := "[Ref_ID: " + entry.ErrorUUID + "]"
			if !strings.Contains(output, want) || strings.Count(output, entry.ErrorUUID) != 1 {
				t.Fatalf("console must include the full reference once: %q", output)
			}
			if output != Console(entry)+"\n" || strings.Count(output, "\n") != 1 {
				t.Fatalf("console replay must preserve the same single line: %q", output)
			}
			if tc.name == "long" && (!strings.HasSuffix(strings.TrimSpace(output), "...") || strings.Contains(output, tc.content)) {
				t.Fatal("long diagnostics were not truncated")
			}
		})
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
