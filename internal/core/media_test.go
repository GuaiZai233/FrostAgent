package core

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestWithoutInlineMediaReplacesOnlyAudioAndVideo(t *testing.T) {
	audio := base64.StdEncoding.EncodeToString([]byte("synthetic-audio"))
	video := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("synthetic-video"))
	parts := []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/gif;base64,R0lGODlh"}},
		{Type: "input_audio", InputAudio: &InputAudio{Data: audio, Format: "mp3"}},
		{Type: "video_url", VideoURL: &VideoURL{URL: video}},
	}
	messages := []ChatMessage{{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: parts}}
	if !HasInlineMedia(messages) {
		t.Fatal("media not detected")
	}
	redacted := WithoutInlineMedia(messages)
	if HasInlineMedia(redacted) {
		t.Fatal("media survived redaction")
	}
	got := redacted[1].Content.([]ContentPart)
	if got[0].Text != "hello" || got[1].ImageURL == nil {
		t.Fatalf("text or image changed: %+v", got)
	}
	if !strings.HasPrefix(got[2].Text, "[audio omitted: format=mp3, size=15 bytes, sha256=") ||
		!strings.HasPrefix(got[3].Text, "[video omitted: type=video/mp4, size=15 bytes, sha256=") {
		t.Fatalf("unexpected placeholders: %q / %q", got[2].Text, got[3].Text)
	}
	if parts[2].InputAudio == nil || messages[1].Content.([]ContentPart)[3].VideoURL == nil {
		t.Fatal("redaction mutated the caller's request")
	}
}

func TestWithoutInlineMediaSharesMediaFreeMessages(t *testing.T) {
	messages := []ChatMessage{{Role: RoleUser, Content: "text only"}}
	if redacted := WithoutInlineMedia(messages); &redacted[0] != &messages[0] {
		t.Fatal("media-free messages should not be copied")
	}
}
