package onebot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

func TestInboundStickerOnlyClassification(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		want          bool
	}{
		{"napcat", `[{"type":"image","data":{"sub_type":1}}]`, true},
		{"llbot", `[{"type":"image","data":{"subType":"1"}}]`, true},
		{"flag", `[{"type":"image","data":{"is_sticker":true}}]`, true},
		{"mface", `[{"type":"mface","data":{}}]`, true},
		{"sticker", `[{"type":"sticker","data":{}}]`, true},
		{"market metadata", `[{"type":"image","data":{"emoji_id":"synthetic"}}]`, true},
		{"multiple and whitespace", `[{"type":"text","data":{"text":" \n"}},{"type":"mface"},{"type":"image","data":{"sub_type":1}}]`, true},
		{"quoted text ignored", `[{"type":"reply","data":{"id":"synthetic-quote","text":"hello"}},{"type":"mface"}]`, true},
		{"ordinary png", `[{"type":"image","data":{"file":"photo.png"}}]`, false},
		{"ordinary gif", `[{"type":"image","data":{"file":"photo.gif","sub_type":0}}]`, false},
		{"mixed text", `[{"type":"text","data":{"text":"hello"}},{"type":"mface"}]`, false},
		{"mixed image", `[{"type":"image","data":{}},{"type":"mface"}]`, false},
		{"emoji", `[{"type":"face","data":{"id":"synthetic"}}]`, false},
		{"unknown", `[{"type":"mface"},{"type":"unknown"}]`, false},
		{"flagged unknown", `[{"type":"mface"},{"type":"unknown","data":{"is_sticker":true}}]`, false},
		{"flagged video", `[{"type":"video","data":{"is_sticker":true}}]`, false},
		{"mention", `[{"type":"at","data":{"qq":"12"}},{"type":"mface"}]`, false},
		{"quoted sticker only", `[{"type":"reply","data":{"message":[{"type":"mface"}]}}]`, false},
		{"literal placeholder", `"[图片]"`, false},
		{"empty", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStickerOnlyEvent(model.OneBotEvent{Message: json.RawMessage(tc.message)}); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestOneBotProactiveInboundStickerSilenceAndImagePreservation(t *testing.T) {
	for _, tc := range []struct {
		name, message             string
		silent, private, noVision bool
	}{
		{name: "sticker", message: `[{"type":"image","data":{"subType":1,"file":"base64://c3RpY2tlcg=="}}]`, silent: true},
		{name: "mface", message: `[{"type":"mface","data":{}}]`, silent: true},
		{name: "sticker without vision", message: `[{"type":"image","data":{"sub_type":1}}]`, silent: true, noVision: true},
		{name: "quote and sticker", message: `[{"type":"reply","data":{"id":"91"}},{"type":"sticker","data":{}}]`, silent: true},
		{name: "ordinary image", message: `[{"type":"image","data":{"file":"base64://cGhvdG8="}}]`},
		{name: "ordinary image without vision", message: `[{"type":"image","data":{"file":"base64://cGhvdG8="}}]`, noVision: true},
		{name: "ordinary gif", message: `[{"type":"image","data":{"file":"base64://R0lGODlh","sub_type":0}}]`},
		{name: "mixed image", message: `[{"type":"mface","data":{}},{"type":"image","data":{"file":"base64://cGhvdG8="}}]`},
		{name: "mixed text", message: `[{"type":"mface","data":{}},{"type":"text","data":{"text":"hello"}}]`},
		{name: "flagged unknown", message: `[{"type":"mface","data":{}},{"type":"unknown","data":{"is_sticker":true}}]`},
		{name: "explicit", message: `[{"type":"at","data":{"qq":"11"}},{"type":"image","data":{"sub_type":1}}]`},
		{name: "private", message: `[{"type":"image","data":{"sub_type":1}}]`, private: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "normal reply"}}}}
			vision := &mockLLMProvider{}
			engine := newTestEngine(provider)
			engine.VisionProvider = vision
			if tc.noVision {
				engine.VisionProvider = nil
			}
			if tc.silent {
				// Buffer only: no store or extraction provider is needed for this assertion.
				engine.MemoryWriter = memory.NewWriter(nil)
			}
			engine.Scope = newTestScopeWithEnv(t, map[string]string{"ENABLE_PROACTIVE_REPLY": "true", "PROACTIVE_REPLY_PROBABILITY": "1", "ENABLE_PROACTIVE_REPLY_WHITELIST": "false", "BOT_NAME": "synthetic-bot", "BOT_ALIASES": "", "ENABLE_AT_IN_GROUP_MSG": "false", "ENABLE_REPLY_IN_GROUP_MSG": "false", "MEMORY_EXTRACT_BATCH_MIN": "100", "MEMORY_EXTRACT_BATCH_MAX": "100"})
			srv, url := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			// Short numeric IDs are synthetic transport fixtures.
			event := model.OneBotEvent{PostType: "message", MessageType: "group", SelfID: 11, UserID: 12, GroupID: 13, MessageID: 1, Message: json.RawMessage(tc.message)}
			if tc.private {
				event.MessageType = "private"
				event.GroupID = 0
			}
			logID, silentLogs := engine.Log().Subscribe(func(entry logs.LogEntry) bool { return entry.Content == "本轮保持沉默: session=group:13" })
			defer engine.Log().Unsubscribe(logID)
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			// A second explicit request provides a response barrier after silent work.
			// Wait for the backend terminal log first to avoid interrupting its lane.
			if tc.silent {
				var lookup model.OneBotAction
				if err := conn.ReadJSON(&lookup); err != nil {
					t.Fatal(err)
				}
				if lookup.Action != "get_group_info" {
					t.Fatalf("unexpected action before silence: %+v", lookup)
				}
				if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": lookup.Echo, "data": map[string]any{"group_id": 13, "group_name": "synthetic"}}); err != nil {
					t.Fatal(err)
				}
				select {
				case <-silentLogs:
				case <-time.After(3 * time.Second):
					t.Fatal("backend silence did not complete")
				}
				session := engine.SessionManager.GetOrCreate("group:13")
				history := session.Snapshot()
				if len(history) != 1 || history[0].Role != "user" {
					t.Fatalf("silence history=%+v", history)
				}
				original := extractUserText(ParseMessageSegments(event.Message), event.Message, engine.Scope)
				historyText, _ := history[0].Content.(string)
				if !strings.HasPrefix(historyText, "User Message: "+original+"\n\n") {
					t.Fatalf("silent turn lost original message %q: %s", original, historyText)
				}
				if count := session.PendingTurnCount(); count != 1 {
					t.Fatalf("pending silent turns=%d want=1", count)
				}
				batch, ready := session.EnqueuePendingTurn([]memory.PendingExtractionItem{{MessageID: "synthetic-barrier"}}, 1, 1)
				if !ready || len(batch.Items) != 3 || batch.Items[0].Message.Content != original || batch.Items[1].Message.Content != "[stay_silent]" {
					t.Fatalf("silent extraction batch=%+v ready=%v", batch, ready)
				}
				next := event
				next.MessageID = 2
				next.Message = json.RawMessage(`[{"type":"at","data":{"qq":"11"}},{"type":"text","data":{"text":"next"}}]`)
				if err := conn.WriteJSON(next); err != nil {
					t.Fatal(err)
				}
			}
			for {
				var action model.OneBotAction
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Action == "get_group_info" {
					_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": 13, "group_name": "synthetic"}})
					continue
				}
				if action.Action != "send_group_msg" && action.Action != "send_private_msg" {
					t.Fatalf("unexpected action (including quote lookup): %+v", action)
				}
				_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 3}})
				break
			}
			provider.mu.Lock()
			calls := len(provider.requests)
			requests := append([]core.ChatRequest(nil), provider.requests...)
			provider.mu.Unlock()
			if calls != 1 {
				t.Fatalf("dialogue calls=%d want=1", calls)
			}
			if tc.noVision && !tc.silent {
				messages := requests[0].Messages
				prompt, _ := messages[len(messages)-1].Content.(string)
				if !strings.Contains(prompt, "\n\nUser Message: \n\n") {
					t.Fatalf("ordinary vision-disabled behavior changed: %s", prompt)
				}
			}
			vision.mu.Lock()
			visionCalls := len(vision.requests)
			vision.mu.Unlock()
			if (tc.silent || tc.noVision) && visionCalls != 0 {
				t.Fatalf("silent sticker called vision %d times", visionCalls)
			}
		})
	}
}
