package onebot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"

	"github.com/gorilla/websocket"
)

func TestLoadObservedImageResolvesCurrentMessageAndQuotedAncestors(t *testing.T) {
	currentImage := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x01}
	quotedImage := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	ancestorImage := []byte("GIF89a ancestor")

	conn := newWSConnection(nil)
	event := model.OneBotEvent{MessageType: "group", GroupID: 42, UserID: 10001, MessageID: 500}
	currentSegments := []content.MessageSegment{
		{Type: "text", Data: map[string]any{"text": "看看这张"}},
		{Type: "image", Data: map[string]any{"base64": base64.StdEncoding.EncodeToString(currentImage)}},
	}
	reply := resolvedReplyContext{
		MessageID: "400",
		SessionID: "group:42",
		Segments: []content.MessageSegment{
			{Type: "image", Data: map[string]any{"base64": base64.StdEncoding.EncodeToString(quotedImage)}},
		},
		Ancestors: []resolvedReplyContext{{
			MessageID: "300",
			SessionID: "group:42",
			Segments: []content.MessageSegment{
				{Type: "image", Data: map[string]any{"base64": base64.StdEncoding.EncodeToString(ancestorImage)}},
			},
		}},
	}

	for _, tc := range []struct {
		name string
		id   string
		want []byte
	}{
		{"current message", "500", currentImage},
		{"quoted message", "400", quotedImage},
		{"quoted ancestor", "300", ancestorImage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := conn.loadObservedImage(context.Background(), event, reply, currentSegments, tc.id, 0)
			if err != nil {
				t.Fatalf("load image: %v", err)
			}
			if string(data) != string(tc.want) {
				t.Fatalf("loaded bytes = %q, want %q", data, tc.want)
			}
		})
	}
}

func TestLoadObservedImageSelectsImageIndexAndRejectsUnusableIDs(t *testing.T) {
	first := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x01}
	second := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	conn := newWSConnection(nil)
	event := model.OneBotEvent{MessageType: "group", GroupID: 42, UserID: 10001, MessageID: 500}
	currentSegments := []content.MessageSegment{
		{Type: "image", Data: map[string]any{"base64": base64.StdEncoding.EncodeToString(first)}},
		{Type: "text", Data: map[string]any{"text": "not an image"}},
		{Type: "image", Data: map[string]any{"base64": base64.StdEncoding.EncodeToString(second)}},
	}

	data, err := conn.loadObservedImage(context.Background(), event, resolvedReplyContext{}, currentSegments, "500", 1)
	if err != nil {
		t.Fatalf("load second image: %v", err)
	}
	if string(data) != string(second) {
		t.Fatalf("image index 1 loaded %q, want %q", data, second)
	}

	if _, err := conn.loadObservedImage(context.Background(), event, resolvedReplyContext{}, currentSegments, "500", 2); err == nil {
		t.Fatal("expected out-of-range image index error")
	}

	// Historical IDs must be numeric before the adapter can ask the platform.
	if _, err := conn.loadObservedImage(context.Background(), event, resolvedReplyContext{}, currentSegments, "msg_abc", 0); err == nil ||
		!strings.Contains(err.Error(), "不是数字") {
		t.Fatalf("unexpected non-numeric message_id error: %v", err)
	}
	if _, err := conn.loadObservedImage(context.Background(), event, resolvedReplyContext{}, currentSegments, "", 0); err == nil {
		t.Fatal("expected empty message_id error")
	}
}

func TestLoadObservedImageCountsUnreadableImageSlots(t *testing.T) {
	readable := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x01}
	conn := newWSConnection(nil)
	event := model.OneBotEvent{MessageType: "group", GroupID: 42, UserID: 10001, MessageID: 500}
	segments := []content.MessageSegment{
		// The first model-visible [图片] has no readable source; it must still
		// occupy index 0 instead of shifting the second image into its place.
		{Type: "image", Data: map[string]any{}},
		{Type: "image", Data: map[string]any{"base64": base64.StdEncoding.EncodeToString(readable)}},
	}

	if _, err := conn.loadObservedImage(context.Background(), event, resolvedReplyContext{}, segments, "500", 0); err == nil ||
		!strings.Contains(err.Error(), "no readable source") {
		t.Fatalf("unexpected unreadable image index error: %v", err)
	}

	data, err := conn.loadObservedImage(context.Background(), event, resolvedReplyContext{}, segments, "500", 1)
	if err != nil {
		t.Fatalf("load second image: %v", err)
	}
	if string(data) != string(readable) {
		t.Fatalf("image index 1 loaded %q, want %q", data, readable)
	}
}

func TestWSInspectMessageImageUsesGetMsgAndVision(t *testing.T) {
	t.Run("dedicated vision", func(t *testing.T) { testWSInspectMessageImage(t, true) })
	t.Run("dialogue fallback", func(t *testing.T) { testWSInspectMessageImage(t, false) })
}

func testWSInspectMessageImage(t *testing.T, dedicatedVision bool) {
	t.Helper()
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x03}
	visionProvider := &mockLLMProvider{responses: []*core.ChatResponse{{
		Message: core.ChatMessage{Role: core.RoleAssistant, Content: "历史消息里的图片是一张海报。"},
	}}}
	dialogueProvider := &mockLLMProvider{responses: []*core.ChatResponse{
		{
			Message: core.ChatMessage{
				Role: core.RoleAssistant,
				ToolCalls: []core.ToolCall{{
					ID:   "call-inspect-image",
					Type: "function",
					Function: core.ToolCallFunction{
						Name:      tools.InspectMessageImageToolName,
						Arguments: `{"message_id":"42","image_index":0}`,
					},
				}},
			},
		},
		{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "我看过那张海报了"}},
	}}
	engine := newTestEngine(dialogueProvider)
	if dedicatedVision {
		engine.VisionProvider = visionProvider
	} else {
		dialogueProvider.responses = []*core.ChatResponse{
			dialogueProvider.responses[0], visionProvider.responses[0], dialogueProvider.responses[1],
		}
	}
	engine.ToolRegistry[tools.InspectMessageImageToolName] = tools.InspectMessageImageTool(engine)

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect test websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	event := model.OneBotEvent{
		SelfID:      20001,
		PostType:    "message",
		MessageType: "private",
		UserID:      10001,
		MessageID:   100,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"刚才那张图是什么"}}]`),
	}
	eventBytes, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("send event: %v", err)
	}

	_, lookupBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read get_msg request: %v", err)
	}
	var lookupAction model.OneBotAction
	if err := json.Unmarshal(lookupBytes, &lookupAction); err != nil {
		t.Fatalf("parse get_msg request: %v", err)
	}
	if lookupAction.Action != "get_msg" {
		t.Fatalf("first action = %q, want get_msg", lookupAction.Action)
	}

	lookupResponse, err := json.Marshal(map[string]any{
		"status":  "ok",
		"retcode": 0,
		"echo":    lookupAction.Echo,
		"data": map[string]any{
			"message_id":   42,
			"message_type": "private",
			"user_id":      10001,
			"message": []map[string]any{{
				"type": "image",
				"data": map[string]any{
					"base64": base64.StdEncoding.EncodeToString(imageBytes),
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal get_msg response: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, lookupResponse); err != nil {
		t.Fatalf("send get_msg response: %v", err)
	}

	_, replyBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read final reply: %v", err)
	}
	var replyAction model.OneBotAction
	if err := json.Unmarshal(replyBytes, &replyAction); err != nil {
		t.Fatalf("parse final reply: %v", err)
	}
	if replyAction.Action != "send_private_msg" {
		t.Fatalf("final action = %q, want send_private_msg", replyAction.Action)
	}

	ack, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "echo": replyAction.Echo})
	if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
		t.Fatalf("send final ack: %v", err)
	}

	wantVisionCalls, wantDialogueCalls, finalRequestIndex := 1, 2, 1
	if !dedicatedVision {
		wantVisionCalls, wantDialogueCalls, finalRequestIndex = 0, 3, 2
	}
	if visionProvider.reqCount != wantVisionCalls {
		t.Fatalf("vision provider calls = %d, want %d", visionProvider.reqCount, wantVisionCalls)
	}
	if dialogueProvider.reqCount != wantDialogueCalls {
		t.Fatalf("dialogue provider calls = %d, want %d", dialogueProvider.reqCount, wantDialogueCalls)
	}
	imageProvider := visionProvider
	imageRequestIndex := 0
	if !dedicatedVision {
		imageProvider, imageRequestIndex = dialogueProvider, 1
	}
	imageRequest, err := json.Marshal(imageProvider.requests[imageRequestIndex].Messages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(imageRequest), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(imageBytes)) {
		t.Fatalf("image description request lost image bytes: %s", imageRequest)
	}
	secondRequest, err := json.Marshal(dialogueProvider.requests[finalRequestIndex].Messages)
	if err != nil {
		t.Fatalf("marshal second dialogue request: %v", err)
	}
	if !strings.Contains(string(secondRequest), "历史消息里的图片是一张海报") {
		t.Fatalf("tool result did not reach the dialogue model: %s", string(secondRequest))
	}
}
