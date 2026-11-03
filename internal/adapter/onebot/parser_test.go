package onebot

import (
	"encoding/json"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/model"
)

func TestBotNameWakeMatching(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		"BOT_NAME": "Polar Fox", "BOT_ALIASES": "Frost Agent,霜降,霜狐 Bot,.*",
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})
	tests := []struct {
		name     string
		message  string
		messages string
		private  bool
		want     bool
	}{
		{name: "English name", message: `"hello POLARFOX"`, want: true},
		{name: "English alias", message: `"hello f R o S t a G e N t"`, want: true},
		{name: "text segment", message: `[{"type":"text","data":{"text":"FROST AGENT 你好"}}]`, want: true},
		{name: "mixed alias", message: `"霜狐bOt你好"`, want: true},
		{name: "Chinese alias", message: `"霜降你好"`, want: true},
		{name: "Chinese remains literal", message: `"霜 降你好"`},
		{name: "regex remains literal", message: `"普通聊天"`},
		{name: "non text excluded", message: `[{"type":"image","data":{"file":"FROST AGENT.png"}}]`},
		{name: "separate segments", message: `[{"type":"text","data":{"text":"Frost"}},{"type":"text","data":{"text":"Agent"}}]`},
		{name: "multiple messages", messages: `["普通聊天", "frostagent你好"]`, want: true},
		{name: "separate messages", messages: `["Frost", "Agent"]`},
		{name: "private unchanged", message: `"frostagent"`, private: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := model.OneBotEvent{MessageType: "group", Message: json.RawMessage(tt.message), Messages: json.RawMessage(tt.messages)}
			if tt.private {
				event.MessageType = "private"
			}
			before, _ := json.Marshal(event)
			if got := IsBotNameMentioned(event, scope); got != tt.want {
				t.Fatalf("IsBotNameMentioned = %v, want %v", got, tt.want)
			}
			if tt.want {
				signals := DetectGroupWakeSignalsWithRNG(event, func() float64 {
					t.Fatal("explicit alias wake must not roll proactive probability")
					return 0
				}, scope)
				if !signals.Alias || !signals.Explicit() || signals.Proactive {
					t.Fatalf("expected explicit alias wake, got %+v", signals)
				}
			}
			after, _ := json.Marshal(event)
			if string(before) != string(after) {
				t.Fatal("wake matching changed the message")
			}
		})
	}
}

func TestBotNameWakeUsesInstanceScope(t *testing.T) {
	t.Setenv("BOT_NAME", "Process Bot")
	t.Setenv("BOT_ALIASES", "Process Alias")
	scope := newTestScopeWithEnv(t, map[string]string{"BOT_NAME": "Instance Bot", "BOT_ALIASES": ""})
	for text, want := range map[string]bool{"instancebot": true, "processbot": false, "processalias": false, "frostagent": false} {
		raw, _ := json.Marshal(text)
		if got := IsBotNameMentioned(model.OneBotEvent{MessageType: "group", Message: raw}, scope); got != want {
			t.Fatalf("instance wake matching for %q = %v, want %v", text, got, want)
		}
	}
}
