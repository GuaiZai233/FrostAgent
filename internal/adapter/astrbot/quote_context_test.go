package astrbot

import (
	"encoding/base64"
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/core"
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
		Metadata: map[string]any{"reply_message_id": "44", "quote_lookup_supported": true},
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
		Metadata:    map[string]any{"reply_message_id": "44", "quote_lookup_supported": true},
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
	if visionCalls != 1 {
		t.Fatalf("direct quoted image was lost: vision calls = %d", visionCalls)
	}
	dialogue.mu.Lock()
	defer dialogue.mu.Unlock()
	if len(dialogue.requests) != 1 {
		t.Fatalf("dialogue calls = %d", len(dialogue.requests))
	}
	for _, message := range dialogue.requests[0].Messages {
		prompt, ok := message.Content.(string)
		if !ok || !strings.Contains(prompt, "<reply_context>") {
			continue
		}
		if !strings.Contains(prompt, "图片里是一盘食物") || !strings.Contains(prompt, "44") ||
			strings.Contains(prompt, "【图片内容】：") {
			t.Fatalf("quoted image attributed to current message or lost: %s", prompt)
		}
		return
	}
	t.Fatal("quoted image context missing")
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

func TestOldAstrBotPluginSkipsQuoteLookup(t *testing.T) {
	t.Setenv("ENABLE_QUOTE_TRACE", "true")
	engine := newTestEngine(&mockLLMProvider{})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	event := Event{
		Type: "event", EventType: "message", Platform: "aiocqhttp",
		MessageType: "group", GroupID: "101", UserID: "77",
		MessageID: "45", Content: "你觉得呢？", IsAt: true,
		Metadata: map[string]any{"reply_message_id": "44"},
	}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	var action Action
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_message" {
		t.Fatalf("old plugin received unsupported lookup or no final response: %+v", action)
	}
}

func TestOtherAstrBotPlatformKeepsDirectQuotedImage(t *testing.T) {
	t.Setenv("ENABLE_QUOTE_TRACE", "true")
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
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	event := Event{
		Type: "event", EventType: "message", Platform: "telegram",
		MessageType: "group", GroupID: "101", UserID: "77",
		MessageID: "45", Content: "你觉得呢？", IsAt: true,
		Metadata: map[string]any{"reply_message_id": "44"},
		Attachments: []core.Attachment{{
			Type: core.AttachmentTypeImage, MessageID: "44",
			Content: []byte{0xff, 0xd8, 0xff, 0xd9},
		}},
	}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	var action Action
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_message" {
		t.Fatalf("final action = %+v", action)
	}
	vision.mu.Lock()
	visionCalls := vision.reqCount
	vision.mu.Unlock()
	if visionCalls != 1 {
		t.Fatalf("direct quoted image vision calls = %d", visionCalls)
	}
	dialogue.mu.Lock()
	defer dialogue.mu.Unlock()
	if len(dialogue.requests) != 1 {
		t.Fatalf("dialogue calls = %d", len(dialogue.requests))
	}
	for _, message := range dialogue.requests[0].Messages {
		prompt, ok := message.Content.(string)
		if !ok || !strings.Contains(prompt, "<reply_context>") {
			continue
		}
		if !strings.Contains(prompt, "图片里是一盘食物") || !strings.Contains(prompt, "44") ||
			strings.Contains(prompt, "【图片内容】：") {
			t.Fatalf("quoted image provenance lost: %s", prompt)
		}
		return
	}
	t.Fatal("quoted image context missing")
}

func TestRejectedCrossGroupLookupCannotUseQuotedAttachmentFallback(t *testing.T) {
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
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	event := Event{
		Type: "event", EventType: "message", Platform: "aiocqhttp",
		MessageType: "group", GroupID: "101", UserID: "77",
		MessageID: "45", Content: "你觉得呢？", IsAt: true,
		Metadata: map[string]any{"reply_message_id": "44", "quote_lookup_supported": true},
		Attachments: []core.Attachment{{
			Type: core.AttachmentTypeImage, MessageID: "44",
			Content: []byte{0xff, 0xd8, 0xff, 0xd9},
		}},
	}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	var lookup map[string]any
	if err := conn.ReadJSON(&lookup); err != nil {
		t.Fatal(err)
	}
	if lookup["type"] != "quote_lookup" {
		t.Fatalf("lookup = %+v", lookup)
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "quote_lookup_result", "echo": lookup["echo"],
		"data": map[string]any{
			"message_id": 44, "message_type": "group", "group_id": 202,
			"message": []map[string]any{{"type": "text", "data": map[string]any{"text": "other group"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var action Action
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_message" {
		t.Fatalf("final action = %+v", action)
	}
	vision.mu.Lock()
	calls := vision.reqCount
	vision.mu.Unlock()
	if calls != 0 {
		t.Fatalf("cross-group image reached vision: %d", calls)
	}
	dialogue.mu.Lock()
	defer dialogue.mu.Unlock()
	if len(dialogue.requests) != 1 {
		t.Fatalf("dialogue calls = %d", len(dialogue.requests))
	}
	for _, message := range dialogue.requests[0].Messages {
		prompt, ok := message.Content.(string)
		if ok && strings.Contains(prompt, "<reply_context>") {
			t.Fatalf("cross-group quote entered prompt: %s", prompt)
		}
	}
}

func TestQQQuoteKeepsAvailableImagesWithoutDuplicates(t *testing.T) {
	forwardedBytes := []byte{0xff, 0xd8, 0xff, 0xd9, 0x01}
	lookupBytes := []byte{0xff, 0xd8, 0xff, 0xd9, 0x02}
	for _, tc := range []struct {
		name       string
		imageData  []map[string]any
		wantImages [][]byte
	}{
		{"opaque_file", []map[string]any{{"file": "quoted-cache-token", "sub_type": 1}}, [][]byte{forwardedBytes}},
		{"readable_source_no_duplicate", []map[string]any{{"base64": base64.StdEncoding.EncodeToString(forwardedBytes)}}, [][]byte{forwardedBytes}},
		{"unusable_remote_url", []map[string]any{{"url": "https://example.com/expired-image"}}, [][]byte{forwardedBytes}},
		{"skipped_earlier_image", []map[string]any{
			{"file": "missing-first-image"},
			{"base64": base64.StdEncoding.EncodeToString(forwardedBytes)},
		}, [][]byte{forwardedBytes}},
		{"partial_chain_keeps_lookup_only_image", []map[string]any{
			{"base64": base64.StdEncoding.EncodeToString(lookupBytes)},
			{"file": "second-cache-token"},
		}, [][]byte{forwardedBytes, lookupBytes}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ENABLE_QUOTE_TRACE", "true")
			vision := &mockLLMProvider{responses: []*core.ChatResponse{{
				Message: core.ChatMessage{Role: core.RoleAssistant, Content: "引用图片里的食物"},
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
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			event := Event{
				Type: "event", EventType: "message", Platform: "aiocqhttp",
				MessageType: "group", GroupID: "101", UserID: "77",
				MessageID: "45", Content: "你觉得呢？", IsAt: true,
				Metadata: map[string]any{"reply_message_id": "44", "quote_lookup_supported": true},
				Attachments: []core.Attachment{{
					Type: core.AttachmentTypeImage, MessageID: "44", Content: forwardedBytes,
				}},
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			var lookup map[string]any
			if err := conn.ReadJSON(&lookup); err != nil {
				t.Fatal(err)
			}
			if lookup["type"] != "quote_lookup" {
				t.Fatalf("lookup = %+v", lookup)
			}
			var imageSegments []map[string]any
			for _, data := range tc.imageData {
				imageSegments = append(imageSegments, map[string]any{"type": "image", "data": data})
			}
			if err := conn.WriteJSON(map[string]any{
				"type": "quote_lookup_result", "echo": lookup["echo"],
				"data": map[string]any{
					"message_id": 44, "message_type": "group", "group_id": "101",
					"message": imageSegments,
				},
			}); err != nil {
				t.Fatal(err)
			}
			var action Action
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Action != "send_message" {
				t.Fatalf("final action = %+v", action)
			}
			vision.mu.Lock()
			if len(vision.requests) != 1 {
				vision.mu.Unlock()
				t.Fatalf("vision calls = %d", len(vision.requests))
			}
			visionRequest := vision.requests[0]
			vision.mu.Unlock()
			var imageURLs []string
			for _, message := range visionRequest.Messages {
				blocks, ok := message.Content.([]any)
				if !ok {
					continue
				}
				for _, block := range blocks {
					part, ok := block.(map[string]any)
					if !ok || part["type"] != "image_url" {
						continue
					}
					imageURL, _ := part["image_url"].(map[string]any)
					url, _ := imageURL["url"].(string)
					imageURLs = append(imageURLs, url)
				}
			}
			if len(imageURLs) != len(tc.wantImages) {
				t.Fatalf("vision images = %v, want %d images", imageURLs, len(tc.wantImages))
			}
			for i, imageBytes := range tc.wantImages {
				wantURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(imageBytes)
				if imageURLs[i] != wantURL {
					t.Fatalf("vision image %d = %q, want %q", i, imageURLs[i], wantURL)
				}
			}
			dialogue.mu.Lock()
			defer dialogue.mu.Unlock()
			if len(dialogue.requests) != 1 {
				t.Fatalf("dialogue calls = %d", len(dialogue.requests))
			}
			for _, message := range dialogue.requests[0].Messages {
				prompt, ok := message.Content.(string)
				if !ok || !strings.Contains(prompt, "<reply_context>") {
					continue
				}
				if !strings.Contains(prompt, "引用图片里的食物") || strings.Contains(prompt, "【图片内容】：") {
					t.Fatalf("quoted image provenance lost: %s", prompt)
				}
				return
			}
			t.Fatal("quoted image context missing")
		})
	}
}
