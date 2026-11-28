package onebot

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/gorilla/websocket"
)

func TestResolveLocalImageSegmentsUsesStoredStickerForOneBotFileName(t *testing.T) {
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	data := []byte("synthetic image bytes")
	id := sticker.HashBytes(data)
	if err := store.Add(id, id+".jpg", data); err != nil {
		t.Fatalf("add sticker: %v", err)
	}
	fileName := fmt.Sprintf("%x.png", md5.Sum(data))
	conn := &wsConnection{stealer: sticker.NewStealer(store, nil)}

	segments := []content.MessageSegment{{
		Type: "image",
		Data: map[string]any{"file": fileName, "sub_type": 1},
	}}
	resolved := conn.resolveLocalImageSegments(segments)
	encoded, ok := resolved[0].Data["base64"].(string)
	if !ok {
		t.Fatalf("resolved segment has no base64 source: %+v", resolved[0].Data)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != string(data) {
		t.Fatalf("resolved base64 = (%q, %v), want stored bytes", encoded, err)
	}
	if _, ok := segments[0].Data["base64"]; ok {
		t.Fatal("resolving segments mutated the input")
	}
}

func TestResolveReplyResponseHydratesStoredStickerFileName(t *testing.T) {
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	data := []byte("synthetic quoted image bytes")
	id := sticker.HashBytes(data)
	if err := store.Add(id, id+".jpg", data); err != nil {
		t.Fatalf("add sticker: %v", err)
	}
	fileName := fmt.Sprintf("%x.png", md5.Sum(data))
	conn := &wsConnection{stealer: sticker.NewStealer(store, nil)}
	conn.rememberMessageSession(42, "private:202")
	event := model.OneBotEvent{SelfID: 101, UserID: 202, MessageType: "private"}
	responseData, err := json.Marshal(map[string]any{
		"message_id":   42,
		"message_type": "private",
		"user_id":      101,
		"target_id":    202,
		"message": []map[string]any{{
			"type": "image",
			"data": map[string]any{"file": fileName, "sub_type": 1},
		}},
	})
	if err != nil {
		t.Fatalf("marshal get_msg data: %v", err)
	}
	reply := conn.resolveReplyResponse(event, 42, oneBotAPIResponse{Status: "ok", Data: responseData})
	if reply.MessageID != "42" || len(reply.Segments) != 1 {
		t.Fatalf("resolved reply = %+v, want one trusted segment", reply)
	}
	encoded := content.SegmentImageSource(reply.Segments[0])
	if encoded != "base64://"+base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("resolved source = %q, want stored bytes", encoded)
	}
}

func TestBanQuotedStickerMarksStoredMD5OnlyImage(t *testing.T) {
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	data := []byte("GIF89a quoted sticker")
	id := sticker.HashBytes(data)
	if err := store.Add(id, id+".gif", data); err != nil {
		t.Fatalf("add sticker: %v", err)
	}
	fileName := fmt.Sprintf("%x.png", md5.Sum(data))

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var action model.OneBotAction
			if json.Unmarshal(raw, &action) != nil || action.Action != "get_msg" {
				continue
			}
			_ = ws.WriteJSON(map[string]any{
				"status": "ok", "retcode": 0, "echo": action.Echo,
				"data": map[string]any{
					"message_id": 42, "message_type": "private", "user_id": 101, "target_id": 202,
					"message": []map[string]any{{"type": "image", "data": map[string]any{"file": fileName, "sub_type": 1}}},
				},
			})
		}
	}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	conn := newWSConnection(client)
	conn.stealer = sticker.NewStealer(store, nil)
	defer conn.Close()
	go func() {
		for {
			_, raw, readErr := client.ReadMessage()
			if readErr != nil {
				return
			}
			conn.handleAPIResponse(raw)
		}
	}()

	event := model.OneBotEvent{
		SelfID: 101, UserID: 202, MessageType: "private",
		Message: json.RawMessage(`[{"type":"reply","data":{"id":"42"}},{"type":"text","data":{"text":"/bansticker"}}]`),
	}
	markedID, err := conn.banQuotedSticker(context.Background(), event)
	if err != nil || markedID != id {
		t.Fatalf("banQuotedSticker = (%q, %v), want stored sticker %q", markedID, err, id)
	}
	entry, ok := store.Get(id)
	if !ok || !entry.ManualBlocked {
		t.Fatalf("stored sticker entry = (%+v, %v), want manual_blocked", entry, ok)
	}
}
