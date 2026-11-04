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

func TestReplyEchoGuardOneBotStructuredFinal(t *testing.T) {
	var guard ReplyEchoGuard
	const duplicate = `{"messages":[{"type":"plain","text":"hello world"}]}`
	if guard.IsOneBotDuplicate(duplicate) {
		t.Fatal("unconfirmed delivery suppressed")
	}
	guard.Record(`{"suppress_auto_mention":true,"messages":[{"type":"quote"},{"type":"plain","text":"hello world"}]}`)
	for _, final := range []string{
		duplicate,
		`{"messages":[{"type":"plain","text":"hello"},{"type":"plain","text":" world"}]}`,
		`{"messages":[{"type":"plain","text":" \nhello world\n"}]}`,
	} {
		if !guard.IsOneBotDuplicate(final) || guard.IsDuplicate(final) {
			t.Errorf("structured duplicate not restricted to OneBot: %s", final)
		}
	}
	for _, final := range []string{
		`not-json`, `{}`, `{"messages":[]}`, duplicate + ` {}`,
		`{"messages":[{"type":"plain","text":42}]}`,
		`{"messages":[{"type":"plain","text":"hello world!"}]}`,
		`{"messages":[{"type":"plain","text":"hello world"},{"type":"plain","text":"extra"}]}`,
		`{"messages":[{"type":"plain","text":"hello world"},{"type":"image","url":"fixture"}]}`,
		`{"messages":[{"type":"plain","text":"hello world","unknown":"extra"}]}`,
		`{"messages":[{"type":"quote","message_id":"fixture"},{"type":"plain","text":"hello world"}]}`,
	} {
		if guard.IsOneBotDuplicate(final) {
			t.Errorf("different or unsupported content suppressed: %s", final)
		}
	}
}
