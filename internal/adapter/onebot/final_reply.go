package onebot

import (
	"encoding/json"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

// parseFinalMessages is shared by rendering and echo detection, including the
// renderer's existing use of partially decoded messages on a field type error.
func parseFinalMessages(final string) ([]tools.Msg, error) {
	var output struct {
		Messages []tools.Msg `json:"messages"`
	}
	err := json.Unmarshal([]byte(final), &output)
	return output.Messages, err
}

func plainFinalReplyText(final string) (string, bool) {
	messages, _ := parseFinalMessages(final)
	if len(messages) == 0 {
		return final, true
	}
	for _, message := range messages {
		// Do not inspect media paths or normalize quotes/mentions into plain text.
		if message.Type != "plain" || message.IsSticker {
			return "", false
		}
	}
	segments, err := tools.BuildOneBotMessage(messages)
	if err != nil || len(segments) == 0 {
		return "", false
	}
	var body strings.Builder
	for _, segment := range segments {
		text, ok := segment.Data["text"].(string)
		if segment.Type != "text" || !ok {
			return "", false
		}
		body.WriteString(text)
	}
	return body.String(), true
}
