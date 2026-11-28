package onebot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// Numeric identities are deliberately small synthetic transport fixtures.
func msgIDEvent(message string) model.OneBotEvent {
	return model.OneBotEvent{SelfID: 101, UserID: 202, GroupID: 303, MessageID: 5,
		PostType: "message", MessageType: "group", Message: json.RawMessage(message)}
}

func TestMsgIDCommandRecognition(t *testing.T) {
	for _, tc := range []struct {
		name, message, reply string
		want                 bool
	}{
		{"reply", `[{"type":"reply","data":{"id":"77"}},{"type":"text","data":{"text":"/msgid"}}]`, "77", true},
		{"reply-and-self-at", `[{"type":"reply","data":{"id":"77"}},{"type":"at","data":{"qq":"101"}},{"type":"text","data":{"text":" /msgid "}}]`, "77", true},
		{"no-reply", `"/msgid"`, "", true},
		{"other-at", `[{"type":"reply","data":{"id":"77"}},{"type":"at","data":{"qq":"404"}},{"type":"text","data":{"text":"/msgid"}}]`, "", false},
		{"extra-text", `[{"type":"reply","data":{"id":"77"}},{"type":"text","data":{"text":"/msgid now"}}]`, "", false},
		{"media", `[{"type":"text","data":{"text":"/msgid"}},{"type":"image","data":{"file":"synthetic"}}]`, "", false},
		{"quote-only", `[{"type":"reply","data":{"id":"77"}}]`, "", false},
		{"uppercase", `"/MSGID"`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply, ok := msgIDCommandReplyID(msgIDEvent(tc.message))
			if ok != tc.want || (ok && reply != tc.reply) {
				t.Fatalf("got (%q, %v), want (%q, %v)", reply, ok, tc.reply, tc.want)
			}
		})
	}
	notice := msgIDEvent(`"/msgid"`)
	notice.PostType = "notice"
	if _, ok := msgIDCommandReplyID(notice); ok {
		t.Fatal("non-message event accepted")
	}
}

func TestRecordBotMessageRefLinksLogs(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	defer engine.Cancel()
	store := engine.Log()
	store.LLMResponse("synthetic model output", "group:303")
	llmID := store.LatestEntryID(logs.LLM_RESPONSE, "group:303")
	store.InfoWithConsoleSummary(logs.TOOL, "send_message call", "call", "group:303")
	toolID := store.LatestEntryID(logs.TOOL, "group:303")
	conn := &wsConnection{engine: engine}
	action := model.OneBotAction{Action: "send_group_msg", Params: map[string]any{"group_id": 303,
		"message": []tools.OneBotSegment{{Type: "text", Data: map[string]any{"text": "synthetic reply"}}}}}

	conn.recordBotMessageRef("group:303", action, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":901}`)}, true)
	refID, ok := store.FindMessageRefByMessageID("group:303", "901")
	if !ok {
		t.Fatal("delivered message was not recorded")
	}
	entry, _ := store.FindMessageRef(refID)
	var ref logs.MessageRef
	if err := json.Unmarshal([]byte(entry.Content), &ref); err != nil || ref.LLMResponseLogID != llmID || ref.ToolLogID != toolID ||
		ref.Action != "send_group_msg" || !strings.Contains(string(ref.Message), "synthetic reply") {
		t.Fatalf("unexpected record: %+v %v", ref, err)
	}

	conn.recordBotMessageRef("group:303", action, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":902}`)}, false)
	plainID, _ := store.FindMessageRefByMessageID("group:303", "902")
	plain, _ := store.FindMessageRef(plainID)
	var plainRef logs.MessageRef
	if err := json.Unmarshal([]byte(plain.Content), &plainRef); err != nil || plainRef.ToolLogID != 0 || plainRef.LLMResponseLogID != llmID {
		t.Fatalf("plain reply must not claim a tool log: %+v %v", plainRef, err)
	}

	before := len(store.Snapshot())
	conn.recordBotMessageRef("group:303", model.OneBotAction{Action: "get_msg"}, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":903}`)}, false)
	conn.recordBotMessageRef("group:303", action, oneBotAPIResponse{Data: json.RawMessage(`{}`)}, false)
	(&wsConnection{engine: engine, mock: true}).recordBotMessageRef("group:303", action, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":904}`)}, false)
	var nilConn *wsConnection
	nilConn.recordBotMessageRef("group:303", action, oneBotAPIResponse{}, false)
	if len(store.Snapshot()) != before {
		t.Fatal("non-delivery, missing id or mock traffic was recorded")
	}
}

func TestWSMsgIDReportsRefID(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newAdminTestScope(t, nil)
	engine.Security = security.NewController(t.TempDir())
	defer engine.Cancel()
	refID := engine.Log().RecordMessageRef(logs.MessageRef{Platform: "onebot", SessionID: "group:303", MessageID: "77", Action: "send_group_msg"})

	adapter := NewAdapter(engine)
	server, url, done := startPokeAdapterServer(adapter)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ask := func(message string) string {
		t.Helper()
		if err := conn.WriteJSON(msgIDEvent(message)); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var got model.OneBotAction
		if err := conn.ReadJSON(&got); err != nil {
			t.Fatal(err)
		}
		params, _ := got.Params.(map[string]any)
		segments, _ := params["message"].([]any)
		if got.Action != "send_group_msg" || params["group_id"] != float64(303) || len(segments) != 1 {
			t.Fatalf("unexpected action: %+v", got)
		}
		data, _ := segments[0].(map[string]any)["data"].(map[string]any)
		text, _ := data["text"].(string)
		return text
	}
	if got, want := ask(`[{"type":"reply","data":{"id":"77"}},{"type":"text","data":{"text":"/msgid"}}]`), "[ FrostAgent Debug ]\n当前消息 RefID: "+refID; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := ask(`[{"type":"reply","data":{"id":"78"}},{"type":"text","data":{"text":"/msgid"}}]`); !strings.HasPrefix(got, "[ FrostAgent Debug ]\n未找到") || strings.Contains(got, "fa_msgid_") {
		t.Fatalf("unknown message reply: %q", got)
	}
	if got := ask(`"/msgid"`); !strings.HasPrefix(got, "[ FrostAgent Debug ]\n请回复") {
		t.Fatalf("missing quote reply: %q", got)
	}

	closeRuntimeStatusQuietly(t, conn, done)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 0 || engine.SessionManager.Count() != 0 {
		t.Fatal("/msgid entered conversation pipeline")
	}
}
