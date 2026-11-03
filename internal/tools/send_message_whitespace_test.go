package tools

import (
	"reflect"
	"testing"
)

func TestBuildOneBotMessagePreservesText(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []Msg
		want     []OneBotSegment
	}{
		{
			name:     "plain text",
			messages: []Msg{{Type: "plain", Text: "你理鱼鱼啦！"}},
			want:     []OneBotSegment{{Type: "text", Data: map[string]any{"text": "你理鱼鱼啦！"}}},
		},
		{
			name:     "intentional whitespace",
			messages: []Msg{{Type: "plain", Text: " \t正文\n  "}},
			want:     []OneBotSegment{{Type: "text", Data: map[string]any{"text": " \t正文\n  "}}},
		},
		{
			name:     "adjacent text segments",
			messages: []Msg{{Type: "plain", Text: "first"}, {Type: "plain", Text: "second"}},
			want: []OneBotSegment{
				{Type: "text", Data: map[string]any{"text": "first"}},
				{Type: "text", Data: map[string]any{"text": "second"}},
			},
		},
		{
			name:     "text after quote",
			messages: []Msg{{Type: "quote", MessageID: "synthetic-message"}, {Type: "plain", Text: "正文"}},
			want: []OneBotSegment{
				{Type: "reply", Data: map[string]any{"id": "synthetic-message"}},
				{Type: "text", Data: map[string]any{"text": "正文"}},
			},
		},
		{
			name:     "intentional mention separator",
			messages: []Msg{{Type: "mention_user", MentionUserID: "synthetic-user"}, {Type: "plain", Text: " 正文"}},
			want: []OneBotSegment{
				{Type: "at", Data: map[string]any{"qq": "synthetic-user"}},
				{Type: "text", Data: map[string]any{"text": " 正文"}},
			},
		},
		{
			name:     "text around media",
			messages: []Msg{{Type: "plain", Text: "before"}, {Type: "image", URL: "https://example.invalid/image.png"}, {Type: "plain", Text: "after"}},
			want: []OneBotSegment{
				{Type: "text", Data: map[string]any{"text": "before"}},
				{Type: "image", Data: map[string]any{"file": "https://example.invalid/image.png"}},
				{Type: "text", Data: map[string]any{"text": "after"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildOneBotMessage(tc.messages)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("segments = %#v, want %#v", got, tc.want)
			}
		})
	}
}
