package onebot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/model"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWSQuotedImageUsesVisionDescriptionInReplyContext(t *testing.T) {
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	}))
	defer imageServer.Close()

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
			"user_id":      112233,
			"message": []map[string]interface{}{{
				"type": "image",
				"data": map[string]interface{}{"url": imageServer.URL + "/quoted.jpg"},
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
