package astrbot

import (
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/gorilla/websocket"
)

func TestInboundStickerOnlyClassification(t *testing.T) {
	sticker := core.Attachment{Type: core.AttachmentTypeImage, SubType: 1, MessageID: "current"}
	photo := core.Attachment{Type: core.AttachmentTypeImage, MessageID: "current", URL: "https://example.invalid/photo.gif"}
	quoted := sticker
	quoted.MessageID = "quoted"
	for _, tc := range []struct {
		name  string
		event Event
		want  bool
	}{
		{"sticker with complete slots", Event{Attachments: []core.Attachment{sticker}, ImageSlotCounts: map[string]int{"current": 1}}, true},
		{"old plugin without slot evidence", Event{Attachments: []core.Attachment{sticker}}, false},
		{"ordinary gif", Event{Attachments: []core.Attachment{photo}}, false},
		{"mixed image", Event{Attachments: []core.Attachment{sticker, photo}}, false},
		{"mixed text", Event{Content: "hello", Attachments: []core.Attachment{sticker}}, false},
		{"quoted only", Event{Attachments: []core.Attachment{quoted}}, false},
		{"quoted plus photo", Event{Attachments: []core.Attachment{quoted, photo}}, false},
		{"downloaded sticker with lost photo", Event{Attachments: []core.Attachment{sticker}, ImageSlotCounts: map[string]int{"current": 2}}, false},
		{"explicit false survives dedup", Event{Attachments: []core.Attachment{sticker}, Metadata: map[string]any{"sticker_only": false}}, false},
		{"new plugin unreadable sticker", Event{Metadata: map[string]any{"sticker_only": true}}, true},
		{"new plugin rendered placeholder", Event{Content: "[图片]", Metadata: map[string]any{"sticker_only": true}}, true},
		{"old plugin ambiguous placeholder", Event{Content: "[图片]", Attachments: []core.Attachment{sticker}}, false},
		{"unknown other component", Event{Attachments: []core.Attachment{sticker}, Metadata: map[string]any{"has_other_content": true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.event.MessageID = "current"
			if got := isStickerOnlyEvent(tc.event); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestAstrBotProactiveInboundStickerSilenceAndImagePreservation(t *testing.T) {
	for _, tc := range []struct {
		name                                                                   string
		stickerOnly, unreadable, mixed, explicit, private, disabled, whitelist bool
	}{
		{name: "sticker", stickerOnly: true},
		{name: "unreadable sticker", stickerOnly: true, unreadable: true},
		{name: "ordinary image"},
		{name: "mixed", mixed: true},
		{name: "explicit sticker", stickerOnly: true, explicit: true},
		{name: "private sticker", stickerOnly: true, private: true},
		{name: "disabled proactive", stickerOnly: true, disabled: true},
		{name: "excluded group", stickerOnly: true, whitelist: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "normal reply"}}}}
			vision := &mockLLMProvider{}
			engine := newTestEngine(provider)
			engine.VisionProvider = vision
			settings := map[string]string{"ENABLE_PROACTIVE_REPLY": "true", "PROACTIVE_REPLY_PROBABILITY": "1", "ENABLE_PROACTIVE_REPLY_WHITELIST": "false", "BOT_NAME": "synthetic-bot", "BOT_ALIASES": "", "ENABLE_AT_IN_GROUP_MSG": "false", "ENABLE_REPLY_IN_GROUP_MSG": "false"}
			if tc.disabled {
				settings["ENABLE_PROACTIVE_REPLY"] = "false"
			}
			if tc.whitelist {
				settings["ENABLE_PROACTIVE_REPLY_WHITELIST"] = "true"
				settings["PROACTIVE_REPLY_GROUP_WHITELIST"] = "other-group"
			}
			engine.Scope = newTestScopeWithEnv(t, settings)
			srv, _, url := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			event := Event{Type: "event", EventType: "message", MessageType: "group", Platform: "astrbot", UserID: "synthetic-user", GroupID: "synthetic-group", MessageID: "current", IsWake: tc.explicit, Metadata: map[string]any{"sticker_only": tc.stickerOnly}}
			att := core.Attachment{Type: core.AttachmentTypeImage, MessageID: "current", Content: []byte("synthetic image")}
			if tc.stickerOnly {
				att.SubType = 1
			}
			event.Attachments = []core.Attachment{att}
			if tc.stickerOnly && !tc.explicit && !tc.private {
				event.Metadata["reply_message_id"] = "quoted"
				event.Metadata["quote_lookup_supported"] = true
				quoted := att
				quoted.MessageID = "quoted"
				quoted.SubType = 0
				event.Attachments = append(event.Attachments, quoted)
			}
			if tc.mixed {
				extra := att
				extra.SubType = 1
				event.Attachments = append(event.Attachments, extra)
			}
			if tc.unreadable {
				event.Attachments = nil
				event.Metadata = map[string]any{"sticker_only": true}
			}
			if tc.private {
				event.MessageType = "private"
				event.GroupID = ""
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			var action Action
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			forced := tc.stickerOnly && !tc.explicit && !tc.private && !tc.disabled && !tc.whitelist
			if forced || tc.disabled || tc.whitelist {
				if action.Action != "noop" || action.SuppressLLM != forced || action.Echo != "reply_current" || action.Content != "" {
					t.Fatalf("terminal action=%+v", action)
				}
			} else if action.Action != "send_message" {
				t.Fatalf("ordinary path lost: %+v", action)
			}
			provider.mu.Lock()
			calls := len(provider.requests)
			provider.mu.Unlock()
			vision.mu.Lock()
			visionCalls := len(vision.requests)
			vision.mu.Unlock()
			want := 1
			if forced || tc.disabled || tc.whitelist {
				want = 0
			}
			if calls != want || visionCalls != want {
				t.Fatalf("dialogue=%d vision=%d want=%d", calls, visionCalls, want)
			}
			if forced {
				history := engine.SessionManager.GetOrCreate(sessionKey(event)).Snapshot()
				if len(history) != 1 || history[0].Role != "user" {
					t.Fatalf("silence history=%+v", history)
				}
				// The next explicit turn must see no duplicate terminal noop.
				event.MessageID = "next"
				event.Content = "next"
				event.IsWake = true
				event.Attachments = nil
				event.Metadata = nil
				if err := conn.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Action != "send_message" || action.Echo != "reply_next" {
					t.Fatalf("duplicate silence or stuck lane: %+v", action)
				}
			}
		})
	}
}
