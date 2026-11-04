package proactive

import "testing"

func TestReplyEchoGuard(t *testing.T) {
	var guard ReplyEchoGuard
	if guard.IsDuplicate("hello") || guard.IsDuplicate(" ") {
		t.Fatal("empty guard suppressed output")
	}
	for _, invalid := range []string{
		`not-json`, `{}`, `{"suppress_auto_mention":true,"messages":[]}`,
		`{"suppress_auto_mention":true,"messages":[{"type":"plain","text":"hello"},{"type":"plain","text":" world"}]}`,
		`{"messages":[{"type":"quote"},{"type":"plain","text":"hello world"}]}`,
		`{"suppress_auto_mention":true,"messages":[{"type":"quote"},{"type":"plain","text":"hello world"},{"type":"image"}]}`,
		`{"suppress_auto_mention":true,"messages":[{"type":"quote"},{"type":"plain","text":" "}]}`,
	} {
		guard.Record(invalid)
		if guard.IsDuplicate("hello world") {
			t.Errorf("non-specific/invalid payload recorded: %s", invalid)
		}
	}
	guard.Record(`{"suppress_auto_mention":true,"messages":[{"type":"quote"},{"type":"plain","text":"hello"},{"type":"plain","text":" world"}]}`)
	guard.Record(`{"suppress_auto_mention":true,"messages":[{"type":"quote"},{"type":"plain","text":"another reply"}]}`)
	for _, duplicate := range []string{"hello world", " \nhello world\n", "another reply", "hello world\nanother reply"} {
		if !guard.IsDuplicate(duplicate) {
			t.Errorf("duplicate not detected: %q", duplicate)
		}
	}
	for _, distinct := range []string{"", "hello world!", "hello world plus new information", `{"messages":[{"type":"plain","text":"hello world"},{"type":"image","url":"fixture"}]}`} {
		if guard.IsDuplicate(distinct) {
			t.Errorf("different content suppressed: %q", distinct)
		}
	}
}
