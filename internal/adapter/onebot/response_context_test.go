package onebot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

func TestResponseContextGuidanceTime(t *testing.T) {
	for _, mode := range []string{"private", "group"} {
		for _, proactive := range []bool{false, true} {
			var value map[string]any
			if err := json.Unmarshal([]byte(buildResponseContext(model.OneBotEvent{MessageType: mode}, GroupWakeSignals{Proactive: proactive}, false)), &value); err != nil {
				t.Fatal(err)
			}
			guidance, _ := value["guidance"].(string)
			if !strings.Contains(guidance, "正常回答。当前系统时间：") || !strings.HasSuffix(guidance, "(UTC+8)。") {
				t.Fatalf("missing time for mode=%s proactive=%v: %+v", mode, proactive, value)
			}
		}
	}
}

func TestPrivateResponseGuidanceTime(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	t.Cleanup(engine.Cancel)
	server, url := startWSTestServer(engine)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 101, UserID: 202, MessageID: 1, Message: json.RawMessage(`"guidance probe"`)}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var action model.OneBotAction
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo}); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 1 {
		t.Fatalf("unexpected requests: %d", len(provider.requests))
	}
	request := provider.requests[0]
	for _, message := range request.Messages {
		content, _ := message.Content.(string)
		if message.Role == "system" && strings.Contains(content, "当前系统时间：") {
			t.Fatalf("time remained in system instructions: %s", content)
		}
	}
	content, _ := request.Messages[len(request.Messages)-1].Content.(string)
	if !strings.Contains(content, `"guidance":"请结合用户消息`) || !strings.Contains(content, "必须最终调用 stay_silent 中止此轮对话") || !strings.Contains(content, "正常回答。当前系统时间：") || strings.Count(content, "当前系统时间：") != 1 {
		t.Fatalf("time must appear once in user guidance: %s", content)
	}
}
