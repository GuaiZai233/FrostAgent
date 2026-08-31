package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/core"
	"FrostAgent/internal/model"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/tools"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestLoadObservedStickerPrefersResolvedReplySegments(t *testing.T) {
	imageBytes := []byte("GIF89a quoted sticker")

	conn := &wsConnection{}
	event := model.OneBotEvent{MessageType: "private", UserID: 10001, MessageID: 100}
	reply := resolvedReplyContext{
		MessageID: "42",
		SessionID: "private:10001",
		Segments: []content.MessageSegment{{
			Type: "image",
			Data: map[string]interface{}{
				"sub_type": 1,
				"base64":   base64.StdEncoding.EncodeToString(imageBytes),
			},
		}},
	}

	data, err := conn.loadObservedSticker(context.Background(), event, reply, nil, "42", 0)
	if err != nil {
		t.Fatalf("load resolved reply sticker: %v", err)
	}
	if string(data) != string(imageBytes) {
		t.Fatalf("loaded bytes = %q, want %q", data, imageBytes)
	}
}

func TestPrivateCrossSessionGetMsgIsNotObserved(t *testing.T) {
	const (
		currentPeerID int64 = 10001
		otherPeerID   int64 = 10002
		botID         int64 = 20001
	)
	imageBytes := []byte("GIF89a cross session")
	responseData, err := json.Marshal(map[string]any{
		"message_id":   42,
		"message_type": "private",
		"user_id":      otherPeerID,
		"message": []map[string]any{{
			"type": "image",
			"data": map[string]any{
				"sub_type": 1,
				"base64":   base64.StdEncoding.EncodeToString(imageBytes),
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal get_msg data: %v", err)
	}

	stealer := sticker.NewStealer(nil, nil)
	conn := newWSConnection(nil)
	conn.stealer = stealer
	conn.rememberMessageSession(42, "private:10001")
	event := model.OneBotEvent{
		SelfID:      botID,
		MessageType: "private",
		UserID:      currentPeerID,
	}
	reply := conn.resolveReplyResponse(event, 42, oneBotAPIResponse{
		Status:  "ok",
		RetCode: 0,
		Data:    responseData,
	})
	if reply.MessageID != "" || len(reply.Segments) != 0 {
		t.Fatalf("cross-session get_msg became trusted reply context: %+v", reply)
	}
	conn.observeResolvedReply(event, reply)

	_, _, err = stealer.StealObserved(
		context.Background(),
		"private:10001",
		"42",
		0,
		func(context.Context, string, int) ([]byte, error) { return imageBytes, nil },
	)
	if !errors.Is(err, sticker.ErrStickerNotInScope) {
		t.Fatalf("cross-session sticker error = %v, want ErrStickerNotInScope", err)
	}
}

func TestPrivateBotAuthoredGetMsgRequiresTrustedSession(t *testing.T) {
	const (
		peerID int64 = 10001
		botID  int64 = 20001
	)
	responseData, err := json.Marshal(map[string]any{
		"message_id":   42,
		"message_type": "private",
		"user_id":      botID,
		"message": []map[string]any{{
			"type": "text",
			"data": map[string]any{"text": "bot reply"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal get_msg data: %v", err)
	}
	response := oneBotAPIResponse{Status: "ok", RetCode: 0, Data: responseData}
	event := model.OneBotEvent{SelfID: botID, MessageType: "private", UserID: peerID}
	conn := newWSConnection(nil)

	if reply := conn.resolveReplyResponse(event, 42, response); reply.MessageID != "" {
		t.Fatalf("unmapped bot-authored message became trusted: %+v", reply)
	}
	targetResponseData, err := json.Marshal(map[string]any{
		"message_id":   42,
		"message_type": "private",
		"user_id":      botID,
		"target_id":    peerID,
		"message": []map[string]any{{
			"type": "text",
			"data": map[string]any{"text": "bot reply"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal target-aware get_msg data: %v", err)
	}
	if reply := conn.resolveReplyResponse(event, 42, oneBotAPIResponse{Status: "ok", Data: targetResponseData}); reply.MessageID != "42" {
		t.Fatalf("target-aware bot-authored message was rejected: %+v", reply)
	}
	ackData, err := json.Marshal(map[string]any{"message_id": "42"})
	if err != nil {
		t.Fatalf("marshal send ACK data: %v", err)
	}
	conn.rememberActionMessageSession(oneBotAPIResponse{Data: ackData}, "private:10001")
	if reply := conn.resolveReplyResponse(event, 42, response); reply.MessageID != "42" || reply.SessionID != "private:10001" {
		t.Fatalf("trusted bot-authored message was rejected: %+v", reply)
	}
}

func TestWSExplicitHistoricalStickerUsesGetMsg(t *testing.T) {
	const actorID int64 = 10001
	t.Setenv("ADMIN_QQ_IDS", strconv.FormatInt(actorID, 10))
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x03}

	provider := &mockLLMProvider{responses: []*core.ChatResponse{
		{
			Message: core.ChatMessage{
				Role: core.RoleAssistant,
				ToolCalls: []core.ToolCall{{
					ID:   "call-steal-history",
					Type: "function",
					Function: core.ToolCallFunction{
						Name:      "steal_sticker",
						Arguments: `{"message_id":"42","sticker_index":0}`,
					},
				}},
			},
		},
		{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "已收藏"}},
	}}
	engine := newTestEngine(provider)
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	stealer := sticker.NewStealer(store, nil)
	stealer.Observe("private:10001", "42", 0, nil, false)
	engine.ToolRegistry["steal_sticker"] = tools.StealStickerTool(stealer)

	mux := http.NewServeMux()
	adapter := NewAdapter(engine)
	adapter.SetStealer(stealer)
	mux.HandleFunc("/ws/frostagent", adapter.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/frostagent"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("connect test websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	event := model.OneBotEvent{
		SelfID:      20001,
		PostType:    "message",
		MessageType: "private",
		UserID:      actorID,
		MessageID:   100,
		Message:     json.RawMessage(`[{"type":"text","data":{"text":"偷历史表情"}}]`),
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

	lookupResponse, err := json.Marshal(map[string]interface{}{
		"status":  "ok",
		"retcode": 0,
		"echo":    lookupAction.Echo,
		"data": map[string]interface{}{
			"message_id":   42,
			"message_type": "private",
			"user_id":      actorID,
			"message": []map[string]interface{}{{
				"type": "image",
				"data": map[string]interface{}{
					"sub_type": 1,
					"base64":   base64.StdEncoding.EncodeToString(imageBytes),
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
	wantID := sticker.HashBytes(imageBytes)
	if !store.Exists(wantID) {
		t.Fatalf("historical sticker %s was not collected", wantID)
	}
	ack, _ := json.Marshal(map[string]interface{}{
		"status": "ok", "retcode": 0, "echo": replyAction.Echo,
	})
	if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
		t.Fatalf("send final ack: %v", err)
	}
}

func TestWSQuotedImageUsesVisionDescriptionInReplyContext(t *testing.T) {
	imageBytes := []byte{0xff, 0xd8, 0xff, 0xd9}

	visionProvider := &mockLLMProvider{responses: []*core.ChatResponse{{
		Message: core.ChatMessage{Role: core.RoleAssistant, Content: "被引用图片是一只小狐狸"},
		Usage:   &core.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
	}}}
	dialogueProvider := &mockLLMProvider{responses: []*core.ChatResponse{{
		Message: core.ChatMessage{Role: core.RoleAssistant, Content: "看到了引用图片"},
		Usage:   &core.Usage{PromptTokens: 30, CompletionTokens: 10, TotalTokens: 40},
	}}}
	engine := newTestEngine(dialogueProvider)
	engine.VisionProvider = visionProvider

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("连接测试 WebSocket 失败: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("设置读取超时失败: %v", err)
	}

	event := model.OneBotEvent{
		SelfID:      123456,
		PostType:    "message",
		MessageType: "private",
		UserID:      987654,
		MessageID:   1001,
		Message:     json.RawMessage(`[{"type":"reply","data":{"id":"42"}},{"type":"text","data":{"text":"这张图是什么？"}}]`),
	}
	eventBytes, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("序列化引用事件失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送引用事件失败: %v", err)
	}

	_, lookupBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取 get_msg 请求失败: %v", err)
	}
	var lookupAction model.OneBotAction
	if err := json.Unmarshal(lookupBytes, &lookupAction); err != nil {
		t.Fatalf("解析 get_msg 请求失败: %v", err)
	}
	if lookupAction.Action != "get_msg" {
		t.Fatalf("期望先回查引用消息，实际 action=%q", lookupAction.Action)
	}

	lookupResponse := map[string]interface{}{
		"status":  "ok",
		"retcode": 0,
		"echo":    lookupAction.Echo,
		"data": map[string]interface{}{
			"message_id":   42,
			"message_type": "private",
			"user_id":      event.UserID,
			"message": []map[string]interface{}{{
				"type": "image",
				"data": map[string]interface{}{"base64": base64.StdEncoding.EncodeToString(imageBytes)},
			}},
		},
	}
	lookupResponseBytes, err := json.Marshal(lookupResponse)
	if err != nil {
		t.Fatalf("序列化 get_msg 响应失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, lookupResponseBytes); err != nil {
		t.Fatalf("发送 get_msg 响应失败: %v", err)
	}

	_, replyBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取最终回复失败: %v", err)
	}
	var replyAction model.OneBotAction
	if err := json.Unmarshal(replyBytes, &replyAction); err != nil {
		t.Fatalf("解析最终回复失败: %v", err)
	}
	if replyAction.Action != "send_private_msg" {
		t.Fatalf("期望发送私聊回复，实际 action=%q", replyAction.Action)
	}

	visionProvider.mu.Lock()
	visionCalls := visionProvider.reqCount
	visionProvider.mu.Unlock()
	if visionCalls != 1 {
		t.Fatalf("期望引用图片只调用一次视觉模型，实际=%d", visionCalls)
	}

	dialogueProvider.mu.Lock()
	dialogueCalls := len(dialogueProvider.requests)
	if dialogueCalls != 1 {
		dialogueProvider.mu.Unlock()
		t.Fatalf("期望对话模型调用一次，实际=%d", dialogueCalls)
	}
	dialogueRequest := dialogueProvider.requests[0]
	dialogueProvider.mu.Unlock()

	var requestPrompt string
	for i := len(dialogueRequest.Messages) - 1; i >= 0; i-- {
		if content, ok := dialogueRequest.Messages[i].Content.(string); ok && strings.Contains(content, "<reply_context>") {
			requestPrompt = content
			break
		}
	}
	if requestPrompt == "" {
		t.Fatal("对话模型请求缺少 reply_context")
	}
	if !strings.Contains(requestPrompt, `"message":"[图片]"`) {
		t.Fatalf("reply_context 应保留引用图片占位，实际=%s", requestPrompt)
	}
	if !strings.Contains(requestPrompt, `"image_description":"被引用图片是一只小狐狸"`) {
		t.Fatalf("reply_context 缺少视觉描述，实际=%s", requestPrompt)
	}

	history := engine.SessionManager.GetOrCreate("private:987654").Snapshot()
	if len(history) == 0 {
		t.Fatal("期望保存当前用户消息")
	}
	durablePrompt, _ := history[len(history)-1].Content.(string)
	if strings.Contains(durablePrompt, "image_description") {
		t.Fatalf("引用图片描述不应写入持久会话历史，实际=%s", durablePrompt)
	}

	ackBytes, err := json.Marshal(map[string]interface{}{
		"status":  "ok",
		"retcode": 0,
		"echo":    replyAction.Echo,
	})
	if err != nil {
		t.Fatalf("序列化最终 ACK 失败: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ackBytes); err != nil {
		t.Fatalf("发送最终 ACK 失败: %v", err)
	}
}
