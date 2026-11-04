package proactive

import (
	"encoding/json"
	"io"
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

// IsOneBotDuplicate compares the text rendered by OneBot's structured final
// output path. Other adapters treat JSON as literal text and must not use this.
// Keep non-plain components and unknown fields rather than discard extra content.
func (g *ReplyEchoGuard) IsOneBotDuplicate(final string) bool {
	if len(g.texts) == 0 {
		return false
	}
	var payload struct {
		Messages []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	decoder := json.NewDecoder(strings.NewReader(final))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || len(payload.Messages) == 0 || decoder.Decode(new(any)) != io.EOF {
		return false
	}
	var body strings.Builder
	for _, component := range payload.Messages {
		if component.Type != "plain" {
			return false
		}
		// BuildOneBotMessage preserves each plain segment exactly, without separators.
		body.WriteString(component.Text)
	}
	return g.IsDuplicate(body.String())
}
