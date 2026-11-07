package onebot

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"strings"
)

// Legacy final-message JSON is rendered as a chain rather than ordinary text.
// Filter its plain components without rewriting media, quotes or mention IDs.
func filterStructuredSegmentMarks(text string) string {
	messages, _ := parseFinalMessages(text)
	if len(messages) == 0 {
		return text
	}
	filtered := make([]tools.Msg, 0, len(messages))
	changed := false
	for _, msg := range messages {
		if msg.Type == "plain" {
			clean := strings.Join(replyformat.Split(msg.Text, false), "")
			changed = changed || clean != msg.Text
			msg.Text = clean
			if clean == "" {
				continue
			}
		}
		filtered = append(filtered, msg)
	}
	if !changed {
		return text
	}
	if len(filtered) == 0 {
		return ""
	}
	data, err := json.Marshal(struct {
		Messages []tools.Msg `json:"messages"`
	}{filtered})
	if err != nil {
		return text
	}
	return string(data)
}
