package parity

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestAutomaticMentionSpacing(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{name: "plain", text: "正文", want: " 正文"},
		{name: "space", text: " 正文", want: " 正文"},
		{name: "tab", text: "\t正文", want: "\t正文"},
		{name: "newline", text: "\n正文", want: "\n正文"},
		{name: "unicode space", text: "\u3000正文", want: "\u3000正文"},
		{name: "trailing whitespace", text: "正文 \n", want: " 正文 \n"},
	} {
		for _, quote := range []bool{false, true} {
			for _, mention := range []bool{false, true} {
				t.Run(tc.name+"/quote="+strconv.FormatBool(quote)+"/mention="+strconv.FormatBool(mention), func(t *testing.T) {
					base := []tools.OneBotSegment{
						{Type: "text", Data: map[string]any{"text": tc.text}},
						{Type: "text", Data: map[string]any{"text": "后续正文"}},
					}
					// Short IDs are synthetic transport fixtures.
					got := WrapGroupReplyOneBot(base, 14, 12, quote, mention)
					wantText := tc.text
					if mention {
						wantText = tc.want
					}
					if got[len(got)-2].Data["text"] != wantText || got[len(got)-1].Data["text"] != "后续正文" {
						t.Fatalf("text after decoration = %+v, want first text %q", got, wantText)
					}
					if base[0].Data["text"] != tc.text {
						t.Fatal("decoration mutated caller text")
					}
					if again := WrapGroupReplyOneBot(got, 14, 12, quote, mention); !reflect.DeepEqual(again, got) {
						t.Fatalf("decoration must be idempotent: first=%+v, second=%+v", got, again)
					}
				})
			}
		}
	}
}

func TestAutomaticMentionSpacingWithExistingComponents(t *testing.T) {
	for _, tc := range []struct {
		name string
		base []tools.OneBotSegment
		want string
	}{
		{name: "existing quote", base: []tools.OneBotSegment{
			{Type: "reply", Data: map[string]any{"id": "synthetic-message"}},
			{Type: "text", Data: map[string]any{"text": "正文"}},
		}, want: " 正文"},
		{name: "explicit mention", base: []tools.OneBotSegment{
			{Type: "at", Data: map[string]any{"qq": "12"}},
			{Type: "text", Data: map[string]any{"text": "正文"}},
		}, want: "正文"},
		{name: "image before text", base: []tools.OneBotSegment{
			{Type: "image", Data: map[string]any{"file": "https://example.invalid/image.png"}},
			{Type: "text", Data: map[string]any{"text": "正文"}},
		}, want: "正文"},
		{name: "sticker bypass", base: []tools.OneBotSegment{
			{Type: "mface", Data: map[string]any{"id": "synthetic-sticker"}},
			{Type: "text", Data: map[string]any{"text": "正文"}},
		}, want: "正文"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := WrapGroupReplyOneBot(tc.base, 14, 12, true, true)
			if got[len(got)-1].Data["text"] != tc.want {
				t.Fatalf("decorated message = %+v, want last text %q", got, tc.want)
			}
		})
	}
}
