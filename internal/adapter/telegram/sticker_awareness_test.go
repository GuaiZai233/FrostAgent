package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/chatflow"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

func TestStickerUploadUsesCurrentStoreAndPreservesQuote(t *testing.T) {
	store, err := sticker.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("GIF89a synthetic sticker fixture")
	id := sticker.HashBytes(data)
	if err := store.Add(id, id+".gif", data); err != nil {
		t.Fatal(err)
	}
	var calls int
	a := &Adapter{client: testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			t.Errorf("unexpected method: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		var reply map[string]any
		if err := json.Unmarshal([]byte(r.FormValue("reply_parameters")), &reply); err != nil {
			t.Error(err)
		}
		if r.FormValue("chat_id") != "-43" || reply["message_id"] != float64(2) {
			t.Errorf("lost target/quote: %v", reply)
		}
		file, _, err := r.FormFile("photo")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		got, err := io.ReadAll(file)
		if err != nil || string(got) != string(data) {
			t.Errorf("wrong store image: %q %v", got, err)
		}
		replyJSON(w, true)
	})}
	a.SetStickerStore(store)
	source := "/api/sticker/" + id + "/image"
	err = a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "-43"}, Parts: []chatflow.Part{{Type: "quote", MessageID: "-43/2"}, {Type: "image", IsSticker: true, URL: source}}})
	if err != nil || calls != 1 {
		t.Fatalf("trusted sticker upload: %v calls=%d", err, calls)
	}
	for _, unsafe := range []string{"https://example.invalid" + source, "//example.invalid" + source, "/api/sticker/missing/image", "/api/sticker/" + id + "/other", "/host/file.gif"} {
		err = a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "-43"}, Parts: []chatflow.Part{{Type: "image", IsSticker: true, URL: unsafe}}})
		if err == nil {
			t.Errorf("untrusted sticker accepted: %s", unsafe)
		}
	}
	if calls != 1 {
		t.Fatal("untrusted sticker reached Telegram")
	}
	a.SetStickerStore(nil)
	if _, err := a.stickerBytes(source); err == nil {
		t.Fatal("sticker accepted without instance store")
	}
}

func TestDispatcherAndAwarenessRequireReadyAndKeepTarget(t *testing.T) {
	var requests []map[string]any
	a := &Adapter{config: Config{Enabled: true}, client: testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var params map[string]any
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			t.Error(err)
		}
		requests = append(requests, params)
		replyJSON(w, true)
	})}
	target := llm.AwarenessTargetFor("telegram", "group", "-43")
	parts := []proactive.AwarenessMessage{{Type: "plain", Text: "hello "}, {Type: "mention_user", MentionUserID: "41"}}
	if a.ID() != "telegram" {
		t.Fatal("wrong dispatcher registration")
	}
	if err := a.Send(context.Background(), core.OutgoingMessage{TargetID: "41", Content: "direct"}); err == nil {
		t.Fatal("unready dispatcher sent")
	}
	if err := a.SendAwareness(context.Background(), target, parts, nil); err == nil {
		t.Fatal("unready awareness sent")
	}
	a.ready.Store(true)
	if err := a.Send(context.Background(), core.OutgoingMessage{TargetID: "41", Content: "direct"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SendAwareness(context.Background(), target, parts, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0]["chat_id"] != float64(41) || requests[1]["chat_id"] != float64(-43) || requests[1]["text"] != "hello @41" || requests[1]["entities"] == nil {
		t.Fatalf("wrong direct/awareness destination or mention: %+v", requests)
	}
	a.Close()
	if err := a.SendAwareness(context.Background(), target, parts, nil); err == nil {
		t.Fatal("closed awareness sent")
	}
}
