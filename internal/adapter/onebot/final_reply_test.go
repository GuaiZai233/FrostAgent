package onebot

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

func TestPlainFinalReplyText(t *testing.T) {
	var guard proactive.ReplyEchoGuard
	guard.Record(`{"suppress_auto_mention":true,"messages":[{"type":"quote"},{"type":"plain","text":"hello world"}]}`)
	for _, final := range []string{
		`{"messages":[{"type":"plain","text":"hello world"}]}`,
		`{"messages":[{"type":"plain","text":"hello"},{"type":"plain","text":" world"}]}`,
		`{"messages":[{"type":"plain","text":" \nhello world\n"}]}`,
		`{"messages":[{"type":"plain","text":"hello world","is_sticker":false}]}`,
		`{"extra":"ignored","messages":[{"type":"plain","text":"hello world","custom":{"text":"ignored"},"path":"ignored","url":"ignored","mention_user_id":"fixture"}]}`,
		`{"Messages":[{"Type":"plain","Text":"hello world"}]}`,
		`{"messages":[{"type":"plain","text":"hello world","url":42}]}`,
	} {
		text, ok := plainFinalReplyText(final)
		if !ok || !guard.IsDuplicate(text) || guard.IsDuplicate(final) {
			t.Errorf("rendered duplicate not detected: text=%q ok=%v final=%s", text, ok, final)
		}
	}
	for _, final := range []string{
		`not-json`, `{}`, `{"messages":[]}`, `{"messages":[{"type":"plain","text":"hello world"}]} {}`,
		`{"messages":[{"type":"plain","text":42}]}`,
		`{"messages":[{"type":"plain","text":"hello world!"}]}`,
		`{"messages":[{"type":"plain","text":"hello world"},{"type":"plain","text":"extra"}]}`,
		`{"messages":[{"type":"plain","text":"hello world"},{"type":"image","url":"fixture"}]}`,
		`{"messages":[{"type":"quote","message_id":"fixture"},{"type":"plain","text":"hello world"}]}`,
		`{"messages":[{"type":"mention_user","mention_user_id":"fixture"},{"type":"plain","text":"hello world"}]}`,
	} {
		if text, ok := plainFinalReplyText(final); ok && guard.IsDuplicate(text) {
			t.Errorf("visible content or invalid output suppressed: %s", final)
		}
	}
	if text, ok := plainFinalReplyText("hello world"); !ok || !guard.IsDuplicate(text) {
		t.Fatal("ordinary text echo no longer detected")
	}
}
