package astrbot

import "testing"

func TestBotNameWakeMatching(t *testing.T) {
	scope := newTestScopeWithEnv(t, map[string]string{
		"BOT_NAME": "Polar Fox", "BOT_ALIASES": "Frost Agent,霜降,霜狐 Bot,.*",
		"PROACTIVE_REPLY_PROBABILITY": "1.00",
	})
	tests := []struct {
		name     string
		content  string
		messages []string
		want     bool
	}{
		{name: "English name", content: "hello POLARFOX", want: true},
		{name: "English alias", content: "hello f R o S t a G e N t", want: true},
		{name: "mixed alias", content: "霜狐bOt你好", want: true},
		{name: "Chinese alias", content: "霜降你好", want: true},
		{name: "Chinese remains literal", content: "霜 降你好"},
		{name: "regex remains literal", content: "普通聊天"},
		{name: "multiple messages", messages: []string{"普通聊天", "frostagent你好"}, want: true},
		{name: "separate messages", messages: []string{"Frost", "Agent"}},
		{name: "content and messages stay separate", content: "Frost", messages: []string{"Agent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := Event{MessageType: "group", Content: tt.content, Messages: tt.messages}
			if got := isExplicitWake(&event, scope); got != tt.want {
				t.Fatalf("isExplicitWake = %v, want %v", got, tt.want)
			}
			got := shouldReplyWithRNG(&event, func() float64 {
				if tt.want {
					t.Fatal("explicit alias wake must not roll proactive probability")
				}
				return 1
			}, scope)
			if got != tt.want {
				t.Fatalf("shouldReply = %v, want %v", got, tt.want)
			}
			if event.Metadata["_frostagent_proactive_reply"] == true {
				t.Fatal("alias matching marked proactive")
			}
			if event.Content != tt.content {
				t.Fatal("wake matching changed content")
			}
		})
	}
	t.Run("group reply disabled", func(t *testing.T) {
		disabled := newTestScopeWithEnv(t, map[string]string{
			"BOT_ALIASES": "Frost Agent", "GROUP_REPLY_ON_MENTION": "false",
		})
		event := Event{MessageType: "group", Content: "FROSTAGENT"}
		if isExplicitWake(&event, disabled) || shouldReply(&event, disabled) {
			t.Fatal("alias bypassed group reply switch")
		}
	})
}

func TestBotNameWakeUsesInstanceScope(t *testing.T) {
	t.Setenv("BOT_NAME", "Process Bot")
	t.Setenv("BOT_ALIASES", "Process Alias")
	scope := newTestScopeWithEnv(t, map[string]string{"BOT_NAME": "Instance Bot", "BOT_ALIASES": ""})
	for text, want := range map[string]bool{"instancebot": true, "processbot": false, "processalias": false, "frostagent": false} {
		if got := isBotNameMentioned(text, scope); got != want {
			t.Fatalf("instance wake matching for %q = %v, want %v", text, got, want)
		}
	}
}
