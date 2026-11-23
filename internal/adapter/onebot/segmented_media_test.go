package onebot

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// runMixedMediaSend drives one group turn with the given send_message arguments
// and returns the outbound group actions it produced. rejectFirst makes the first
// outbound action fail with a platform rejection (retcode 403) instead of ACKing.
func runMixedMediaSend(t *testing.T, limit, args string, rejectFirst bool) []model.OneBotAction {
	t.Helper()
	t.Setenv("ENABLE_MULTI_SEGMENT_REPLY", "true")
	t.Setenv("MULTI_SEGMENT_REPLY_MAX_SEGMENTS", limit)
	provider := &mockLLMProvider{responses: []*core.ChatResponse{
		{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "call_media", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args},
		}}}},
		{Message: core.ChatMessage{Role: core.RoleAssistant}},
	}}
	engine := newTestEngine(provider)
	engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()
	event := model.OneBotEvent{SelfID: 700000031, PostType: "message", MessageType: "group", GroupID: 700000032, UserID: 700000033, MessageID: 700000034,
		Message: json.RawMessage(`[{"type":"at","data":{"qq":"700000031"}},{"type":"text","data":{"text":"你好"}}]`)}
	eventBytes, _ := json.Marshal(event)
	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}
	var sent []model.OneBotAction
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var action model.OneBotAction
		if err := json.Unmarshal(raw, &action); err != nil {
			continue
		}
		if action.Action == "get_group_info" {
			reply, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"group_id": event.GroupID, "group_name": "媒体分段测试群"}, "echo": action.Echo})
			_ = conn.WriteMessage(websocket.TextMessage, reply)
			continue
		}
		// Every other outbound action (group messages and file uploads) is recorded
		// and acknowledged so each split part completes.
		sent = append(sent, action)
		ack := map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 9}}
		if rejectFirst && len(sent) == 1 {
			ack = map[string]any{"status": "failed", "retcode": 403, "wording": "synthetic rejection", "echo": action.Echo}
		}
		encoded, _ := json.Marshal(ack)
		_ = conn.WriteMessage(websocket.TextMessage, encoded)
	}
	return sent
}

func segmentsOf(t *testing.T, action model.OneBotAction) []any {
	t.Helper()
	params, _ := action.Params.(map[string]any)
	segs, _ := params["message"].([]any)
	return segs
}

// A caption plain and the image after it stay in one outbound message, so the
// batch becomes first | caption+image.
func TestMixedMediaSendKeepsCaptionWithImage(t *testing.T) {
	args := `{"messages":[{"type":"plain","text":"第一段"},{"type":"plain","text":"第二段"},{"type":"image","url":"https://example.invalid/a.png"}]}`
	sent := runMixedMediaSend(t, "3", args, false)
	if len(sent) != 2 {
		t.Fatalf("want 2 outbound group messages (first | caption+image), got %d", len(sent))
	}
	second := segmentsOf(t, sent[1])
	if len(second) != 2 {
		t.Fatalf("second message must hold caption and image together, got %d segments", len(second))
	}
	if m, _ := second[0].(map[string]any); m["type"] != "text" {
		t.Fatalf("second message must start with the caption text, got %v", m["type"])
	}
	if m, _ := second[1].(map[string]any); m["type"] != "image" {
		t.Fatalf("second message must carry the image, got %v", m["type"])
	}
}

// A platform rejection of the first segment must stop the batch: later segments
// are never sent, so a declared-final turn cannot finish with a missing first part.
func TestMixedMediaSendStopsAfterRejectedSegment(t *testing.T) {
	args := `{"messages":[{"type":"plain","text":"第一段"},{"type":"plain","text":"第二段"},{"type":"image","url":"https://example.invalid/a.png"}]}`
	if sent := runMixedMediaSend(t, "3", args, true); len(sent) != 1 {
		t.Fatalf("after the first segment is rejected, no later segment may be sent; got %d sends", len(sent))
	}
}

// A batch over the segment cap must be rejected before anything is sent.
func TestMixedMediaSendOverLimitDeliversNothing(t *testing.T) {
	args := `{"messages":[{"type":"plain","text":"一"},{"type":"plain","text":"二"},{"type":"plain","text":"三"},{"type":"image","url":"https://example.invalid/a.png"}]}`
	if sent := runMixedMediaSend(t, "2", args, false); len(sent) != 0 {
		t.Fatalf("over-limit batch must deliver nothing, got %d sends", len(sent))
	}
}

// A late invalid file must be caught before the first segment is written.
// The batch is validated as a whole, so a missing file means zero sends.
func TestMixedMediaSendPreflightsBeforeAnySend(t *testing.T) {
	args := `{"messages":[{"type":"plain","text":"第一段"},{"type":"plain","text":"第二段"},{"type":"image","path":"/nonexistent/synthetic-missing.png"}]}`
	if sent := runMixedMediaSend(t, "3", args, false); len(sent) != 0 {
		t.Fatalf("invalid trailing file must fail preflight with zero sends, got %d", len(sent))
	}
}

// A standalone file between captions must not reset the caption grouping. The
// batch is first | file | first image | second caption + second image: the first
// image stays in the first segment, and the second caption still starts a new one.
func TestMixedMediaSendFileDoesNotResetCaptionGrouping(t *testing.T) {
	args := `{"messages":[` +
		`{"type":"plain","text":"first caption"},` +
		`{"type":"file","url":"https://example.invalid/doc.pdf"},` +
		`{"type":"image","url":"https://example.invalid/first.png"},` +
		`{"type":"plain","text":"second caption"},` +
		`{"type":"image","url":"https://example.invalid/second.png"}]}`
	sent := runMixedMediaSend(t, "5", args, false)
	if len(sent) != 4 {
		t.Fatalf("want 4 outbound actions (first | file | first image | second caption+image), got %d", len(sent))
	}
	last := segmentsOf(t, sent[3])
	if len(last) != 2 {
		t.Fatalf("last message must hold the second caption and its image together, got %d segments", len(last))
	}
	if m, _ := last[0].(map[string]any); m["type"] != "text" {
		t.Fatalf("last message must start with the second caption, got %v", m["type"])
	}
	if m, _ := last[1].(map[string]any); m["type"] != "image" {
		t.Fatalf("last message must carry the second image, got %v", m["type"])
	}
}

// A mention prefix after a standalone file must not absorb the next segment's
// caption. The batch is first | file | mention | second caption + image.
func TestMixedMediaSendPrefixDoesNotCrossSegmentBoundary(t *testing.T) {
	args := `{"messages":[` +
		`{"type":"plain","text":"first caption"},` +
		`{"type":"file","url":"https://example.invalid/doc.pdf"},` +
		`{"type":"mention_user","mention_user_id":"700000050"},` +
		`{"type":"plain","text":"second caption"},` +
		`{"type":"image","url":"https://example.invalid/second.png"}]}`
	sent := runMixedMediaSend(t, "5", args, false)
	if len(sent) != 4 {
		t.Fatalf("want 4 outbound actions (first | file | mention | second caption+image), got %d", len(sent))
	}
	last := segmentsOf(t, sent[3])
	if len(last) != 2 {
		t.Fatalf("last message must hold the second caption and its image, got %d segments", len(last))
	}
	if m, _ := last[1].(map[string]any); m["type"] != "image" {
		t.Fatalf("last message must carry the image, got %v", m["type"])
	}
}
