package parity

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestIsReplyToBotMentionOnlyOneBotSegments(t *testing.T) {
	const selfID int64 = 123456
	const otherID int64 = 654321
	reply := tools.OneBotSegment{Type: "reply", Data: map[string]any{"id": "1001"}}
	atSelf := tools.OneBotSegment{Type: "at", Data: map[string]any{"qq": "123456"}}
	atOther := tools.OneBotSegment{Type: "at", Data: map[string]any{"qq": "654321"}}
	blank := tools.OneBotSegment{Type: "text", Data: map[string]any{"text": " "}}
	words := tools.OneBotSegment{Type: "text", Data: map[string]any{"text": "hi"}}
	image := tools.OneBotSegment{Type: "image", Data: map[string]any{"file": "synthetic"}}
	face := tools.OneBotSegment{Type: "face", Data: map[string]any{"id": "1"}}

	cases := []struct {
		name           string
		isGroup        bool
		selfID         int64
		segments       []tools.OneBotSegment
		hasImages      bool
		quotedSenderID int64
		want           bool
	}{
		{"reply to bot and only @bot", true, selfID, []tools.OneBotSegment{reply, atSelf}, false, selfID, true},
		{"whitespace text is ignored", true, selfID, []tools.OneBotSegment{reply, atSelf, blank}, false, selfID, true},
		{"no reply segment", true, selfID, []tools.OneBotSegment{atSelf}, false, selfID, false},
		{"reply to someone else", true, selfID, []tools.OneBotSegment{reply, atSelf}, false, otherID, false},
		{"unresolved quote sender", true, selfID, []tools.OneBotSegment{reply, atSelf}, false, 0, false},
		{"zero self id never matches", true, 0, []tools.OneBotSegment{reply, atSelf}, false, 0, false},
		{"private chat excluded", false, selfID, []tools.OneBotSegment{reply, atSelf}, false, selfID, false},
		{"extra text disqualifies", true, selfID, []tools.OneBotSegment{reply, atSelf, words}, false, selfID, false},
		{"other mention disqualifies", true, selfID, []tools.OneBotSegment{reply, atSelf, atOther}, false, selfID, false},
		{"image disqualifies", true, selfID, []tools.OneBotSegment{reply, atSelf, image}, false, selfID, false},
		{"image flag disqualifies", true, selfID, []tools.OneBotSegment{reply, atSelf}, true, selfID, false},
		{"face disqualifies", true, selfID, []tools.OneBotSegment{reply, atSelf, face}, false, selfID, false},
		{"name-only mention is not @", true, selfID, []tools.OneBotSegment{reply, words}, false, selfID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsReplyToBotMentionOnlyOneBotSegments(tc.isGroup, tc.selfID, tc.segments, tc.hasImages, tc.quotedSenderID)
			if got != tc.want {
				t.Fatalf("IsReplyToBotMentionOnlyOneBotSegments() = %v, want %v", got, tc.want)
			}
		})
	}
}
