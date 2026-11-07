package astrbot

import (
	"reflect"
	"testing"
)

func TestAstrBotAutomaticMentionSpacing(t *testing.T) {
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
	} {
		for _, path := range []string{"content", "messages", "existing quote"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				scope := newAstrBotRuntimeScope(t, map[string]string{
					"ENABLE_AT_IN_GROUP_MSG":    "true",
					"ENABLE_REPLY_IN_GROUP_MSG": "true",
				})
				action := Action{Action: "send_message", MessageType: "group", UserID: "synthetic-user", ReplyMessageID: "synthetic-message", Content: tc.text}
				if path != "content" {
					action.Messages = []ActionMessage{{Type: "plain", Text: tc.text}}
					if path == "existing quote" {
						action.Messages = append([]ActionMessage{{Type: "quote", MessageID: action.ReplyMessageID}}, action.Messages...)
					}
				}
				got := action.withConfiguredGroupMentionScope(scope).withConfiguredGroupReplyScope(scope)
				want := []ActionMessage{
					{Type: "quote", MessageID: action.ReplyMessageID},
					{Type: "mention_user", MentionUserID: action.UserID},
					{Type: "plain", Text: tc.want},
				}
				if !reflect.DeepEqual(got.Messages, want) || got.Content != tc.text {
					t.Fatalf("action = %+v, want messages %+v and original content %q", got, want, tc.text)
				}
				if path != "content" && action.Messages[len(action.Messages)-1].Text != tc.text {
					t.Fatal("decoration mutated caller text")
				}
				if again := got.withConfiguredGroupMentionScope(scope).withConfiguredGroupReplyScope(scope); !reflect.DeepEqual(again, got) {
					t.Fatalf("decoration must be idempotent: first=%+v, second=%+v", got, again)
				}
			})
		}
	}
}
