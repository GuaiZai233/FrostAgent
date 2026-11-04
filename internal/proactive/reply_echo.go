package proactive

import (
	"encoding/json"
	"strings"
)

// ReplyEchoGuard tracks confirmed specific replies within one serial agent run.
// It only removes exact text echoes (ignoring surrounding whitespace), not novel
// follow-ups, media, or semantically similar text. Call Record after delivery.
type ReplyEchoGuard struct {
	texts []string
}

func (g *ReplyEchoGuard) Record(payload string) {
	var message struct {
		SuppressAutoMention bool `json:"suppress_auto_mention"`
		Messages            []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if json.Unmarshal([]byte(payload), &message) != nil || !message.SuppressAutoMention ||
		len(message.Messages) < 2 || message.Messages[0].Type != "quote" {
		return
	}
	var body strings.Builder
	for _, component := range message.Messages[1:] {
		if component.Type != "plain" {
			return
		}
		body.WriteString(component.Text)
	}
	if text := strings.TrimSpace(body.String()); text != "" {
		g.texts = append(g.texts, text)
	}
}

func (g *ReplyEchoGuard) IsDuplicate(final string) bool {
	final = strings.TrimSpace(final)
	if final == "" {
		return false
	}
	for _, text := range g.texts {
		if final == text {
			return true
		}
	}
	return len(g.texts) > 1 && final == strings.Join(g.texts, "\n")
}
