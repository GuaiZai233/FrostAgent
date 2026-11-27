package onebot

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestQQDeliveredImageKeysAlignWithImagePlaceholders(t *testing.T) {
	messages := []tools.Msg{
		{Type: "plain", Text: "hi"},
		{Type: "image", ImageKey: "key-one"},
		{Type: "mface"},
		{Type: "image"},
	}
	keys := qqDeliveredImageKeys(messages)
	if len(keys) != 2 || keys[0] != "key-one" || keys[1] != "" {
		t.Fatalf("keys = %#v, want one entry per image component", keys)
	}
	if got := qqDeliveredImageKeys([]tools.Msg{{Type: "image"}}); got != nil {
		t.Fatalf("keys without any preview = %#v, want nil", got)
	}
}

func TestQQInlineImageBytesOnlyDecodesInlineSources(t *testing.T) {
	payload := []byte("synthetic-bytes")
	encoded := base64.StdEncoding.EncodeToString(payload)
	for _, source := range []string{"base64://" + encoded, "data:image/png;base64," + encoded} {
		if got := qqInlineImageBytes(source); string(got) != string(payload) {
			t.Fatalf("%q decoded to %q", source, got)
		}
	}
	for _, source := range []string{"", "https://example.invalid/a.png", "/tmp/a.png", "data:image/png,raw", "base64://!!"} {
		if got := qqInlineImageBytes(source); got != nil {
			t.Fatalf("%q must not be fetched or decoded, got %q", source, got)
		}
	}
}

func TestGroupCaptureKeepsImageKeysPerPlaceholderAndOutOfPrompt(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAHEC7cQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(png)
	store := logs.New("", "test", 10)
	engine := newTestEngine(nil)
	engine.Scope = &runtimescope.Scope{Logger: store}
	event := model.OneBotEvent{
		PostType: "message", MessageType: "group", GroupID: 40101, UserID: 7001, MessageID: 11,
		Sender:  &model.OneBotSender{Nickname: "synthetic", UserID: 7001},
		Message: json.RawMessage(`[{"type":"image","data":{"file":"unresolvable"}},{"type":"image","data":{"base64":"` + encoded + `"}}]`),
	}
	captureGroupCompactMessage(event, engine)

	snap := engine.SessionManager.GetOrCreate("group:40101").SnapshotGroupContext(20, 4000, "")
	if len(snap.RecentStructuredMessages) != 1 {
		t.Fatalf("messages = %+v", snap.RecentStructuredMessages)
	}
	message := snap.RecentStructuredMessages[0]
	wantKey := logs.SessionImageKey("base64://" + encoded)
	if strings.Count(message.Content, "[图片]") != 2 || len(message.ImageKeys) != 2 || message.ImageKeys[0] != "" || message.ImageKeys[1] != wantKey {
		t.Fatalf("content %q keys %#v, want one entry per placeholder with only the readable image keyed", message.Content, message.ImageKeys)
	}
	if prompt := llm.FormatRecentGroupMessagesContext(snap.RecentStructuredMessages); strings.Contains(prompt, wantKey) || strings.Contains(prompt, "image_keys") {
		t.Fatalf("preview keys leaked into the model prompt: %s", prompt)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !store.HasSessionImage(wantKey) {
		if time.Now().After(deadline) {
			t.Fatal("image was not retained for preview")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestQQSenderPrepareRetainsInlineImagesForPreview(t *testing.T) {
	payload := []byte("synthetic-image-bytes")
	file := filepath.Join(t.TempDir(), "synthetic.png")
	if err := os.WriteFile(file, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	retained := map[string][]byte{}
	sender := qqSender{
		event:       model.OneBotEvent{MessageType: "group", GroupID: 40102},
		retainImage: func(key string, data []byte) { retained[key] = data },
	}
	parts, err := sender.prepare([]tools.Msg{
		{Type: "image", Path: file, IsSticker: true},
		{Type: "image", URL: "https://example.invalid/remote.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || len(parts[0].messages) != 2 {
		t.Fatalf("parts = %+v", parts)
	}
	key := logs.SessionImageKey(string(payload))
	if parts[0].messages[0].ImageKey != key || string(retained[key]) != string(payload) {
		t.Fatalf("inline image not retained: key %q retained %v", parts[0].messages[0].ImageKey, retained)
	}
	if parts[0].messages[1].ImageKey != "" || len(retained) != 1 {
		t.Fatal("remote image URL must not be fetched or keyed")
	}
}
