package astrbot

import (
	"FrostAgent/internal/core"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestQuoteTraceThroughAstrBotPluginProtocol(t *testing.T) {
	t.Setenv("ENABLE_QUOTE_TRACE", "true")
	t.Setenv("QUOTE_TRACE_DEPTH", "5")
	vision := &mockLLMProvider{responses: []*core.ChatResponse{{
		Message: core.ChatMessage{Role: core.RoleAssistant, Content: "图片里是一盘食物"},
		Usage:   &core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}}}
	dialogue := &mockLLMProvider{}
	engine := newTestEngine(dialogue)
	engine.VisionProvider = vision
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	event := Event{
		Type: "event", EventType: "message", Platform: "aiocqhttp",
		MessageType: "group", GroupID: "101", UserID: "77",
		MessageID: "45", Content: "你觉得呢？", IsAt: true,
		Metadata: map[string]any{"reply_message_id": "44"},
	}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	for id := 44; id >= 41; id-- {
		var lookup struct {
			Type           string `json:"type"`
			Echo           string `json:"echo"`
			EventMessageID string `json:"event_message_id"`
			MessageID      string `json:"message_id"`
		}
		if err := conn.ReadJSON(&lookup); err != nil {
			t.Fatal(err)
		}
		if lookup.Type != "quote_lookup" || lookup.MessageID != stringID(id) || lookup.EventMessageID != event.MessageID {
			t.Fatalf("lookup = %+v, want quote_lookup for %d", lookup, id)
		}
		var message []map[string]any
		if id == 41 {
			message = []map[string]any{{"type": "image", "data": map[string]any{
				"base64": base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0xd9}),
			}}}
		} else {
			message = []map[string]any{
				{"type": "reply", "data": map[string]any{"id": id - 1}},
				{"type": "text", "data": map[string]any{"text": "同意"}},
			}
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "quote_lookup_result", "echo": lookup.Echo,
			"data": map[string]any{
				"message_id": id, "message_type": "group",
				"group_id": event.GroupID, "user_id": "78",
				"message": message,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	var final Action
	if err := conn.ReadJSON(&final); err != nil {
		t.Fatal(err)
	}
	if final.Action != "send_message" {
		t.Fatalf("final action = %+v", final)
	}
	dialogue.mu.Lock()
	defer dialogue.mu.Unlock()
	if len(dialogue.requests) != 1 {
		t.Fatalf("dialogue calls = %d", len(dialogue.requests))
	}
	found := false
	for _, message := range dialogue.requests[0].Messages {
		prompt, ok := message.Content.(string)
		if !ok || !strings.Contains(prompt, "<reply_context>") {
			continue
		}
		found = true
		if !strings.Contains(prompt, `"message_id":"41"`) ||
			!strings.Contains(prompt, `"image_description":"图片里是一盘食物"`) ||
			!strings.Contains(prompt, `"quoted_messages"`) {
			t.Fatalf("incomplete quote chain: %s", prompt)
		}
	}
	if !found {
		t.Fatal("reply_context missing")
	}
	vision.mu.Lock()
	defer vision.mu.Unlock()
	if vision.reqCount != 1 {
		t.Fatalf("vision calls = %d", vision.reqCount)
	}
}

func stringID(value int) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func TestParseAstrBotQuoteRejectsOtherGroup(t *testing.T) {
	event := Event{MessageType: "group", GroupID: "101"}
	data := json.RawMessage(`{"message_id":44,"message_type":"group","group_id":"202","message":[{"type":"text","data":{"text":"private"}}]}`)
	if _, ok := parseAstrBotQuote(event, "44", data); ok {
		t.Fatal("cross-group quote was accepted")
	}
}

func TestParseAstrBotQuoteRejectsGroupMessageInPrivate(t *testing.T) {
	event := Event{MessageType: "private", UserID: "77"}
	data, _ := json.Marshal(map[string]any{
		"message_id": 44, "message_type": "private",
		"group_id": 101, "user_id": 77,
		"message": []map[string]any{{"type": "text", "data": map[string]any{"text": "other session"}}},
	})
	if _, ok := parseAstrBotQuote(event, "44", data); ok {
		t.Fatal("group message became trusted as private quote")
	}
}

func TestQuotedAttachmentIsNotCurrentImageWhenLookupFails(t *testing.T) {
	t.Setenv("ENABLE_QUOTE_TRACE", "true")
	vision := &mockLLMProvider{}
	dialogue := &mockLLMProvider{}
	engine := newTestEngine(dialogue)
	engine.VisionProvider = vision
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	event := Event{
		Type: "event", EventType: "message", Platform: "aiocqhttp",
		MessageType: "group", GroupID: "101", UserID: "77",
		MessageID: "45", Content: "你觉得呢？", IsAt: true,
		Metadata:    map[string]any{"reply_message_id": "44"},
		Attachments: []core.Attachment{{Type: core.AttachmentTypeImage, MessageID: "44", Content: []byte{0xff, 0xd8, 0xff, 0xd9}}},
	}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	var lookup struct {
		Type string `json:"type"`
		Echo string `json:"echo"`
	}
	if err := conn.ReadJSON(&lookup); err != nil {
		t.Fatal(err)
	}
	if lookup.Type != "quote_lookup" {
		t.Fatalf("lookup type = %q", lookup.Type)
	}
	if err := conn.WriteJSON(map[string]any{"type": "quote_lookup_result", "echo": lookup.Echo, "data": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	var final Action
	if err := conn.ReadJSON(&final); err != nil {
		t.Fatal(err)
	}
	if final.Action != "send_message" {
		t.Fatalf("final action = %q", final.Action)
	}
	vision.mu.Lock()
	visionCalls := vision.reqCount
	vision.mu.Unlock()
	if visionCalls != 0 {
		t.Fatalf("historical image treated as current: vision calls = %d", visionCalls)
	}
}

func TestParseAstrBotQuotePreservesLargeNumericIDs(t *testing.T) {
	const id = "9007199254740993"
	const parent = "9007199254740995"
	event := Event{MessageType: "group", GroupID: id}
	raw, err := json.Marshal(map[string]any{
		"message_id": json.Number(id), "message_type": "group",
		"group_id": json.Number(id), "user_id": json.Number(id),
		"message": []map[string]any{{
			"type": "reply", "data": map[string]any{"id": json.Number(parent)},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	node, ok := parseAstrBotQuote(event, id, raw)
	if !ok || node.ParentID != parent || node.Context["message_id"] != id {
		t.Fatalf("large numeric IDs lost: node=%+v, ok=%v", node, ok)
	}
}
