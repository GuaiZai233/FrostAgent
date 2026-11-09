package llm

import "testing"

// A confirmed send with no speakable text (image, file, audio) is a public act,
// not silence: SpokeText must not be empty, while a turn with no send stays empty.
func TestPublicReplyLogMediaOnlyDeliveryIsNotSilence(t *testing.T) {
	session := NewSessionManager().GetOrCreate("group:media-only")

	silent := NewPublicReplyLog(session, session.Epoch())
	if got := silent.SpokeText(); got != "" {
		t.Fatalf("no delivery: SpokeText=%q want empty", got)
	}

	media := NewPublicReplyLog(session, session.Epoch())
	media.NoteConfirmed()
	if got := media.SpokeText(); got == "" {
		t.Fatal("confirmed media-only send treated as silence")
	}
	if media.Text() != "" {
		t.Fatalf("media-only send must not invent delivered text, got %q", media.Text())
	}
}
