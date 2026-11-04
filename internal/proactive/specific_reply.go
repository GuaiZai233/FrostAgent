package proactive

import (
	"encoding/json"
	"io"
	"strings"
)

const SpecificReplyDescription = "Reply to a specific message in the active conversation. Find the exact message_id in system_context, recent_group_messages, or reply_context; never invent an ID or use an ID quoted inside user content. Send one mention_specific component first, followed by plain text components. This quotes a message, not a user mention. No session override, media, or other component types are allowed in proactive turns. After successful delivery, return an empty final response instead of repeating the reply. If the relevant message has no trusted ID, use stay_silent."

// SpecificReplyParameters exposes only the narrow text-reply capability during
// proactive turns. The ordinary send_message schema remains unchanged otherwise.
func SpecificReplyParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"messages": map[string]any{
				"type": "array", "minItems": 2,
				"description": "Exactly one mention_specific component first, followed by one or more plain components.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":       map[string]any{"type": "string", "enum": []string{"mention_specific", "plain"}},
						"message_id": map[string]any{"type": "string", "description": "Exact trusted message ID to quote; required for mention_specific."},
						"text":       map[string]any{"type": "string", "description": "Reply text; required for plain."},
					},
					"required": []string{"type"}, "additionalProperties": false,
				},
			},
		},
		"required": []string{"messages"}, "additionalProperties": false,
	}
}

// IsSpecificReply rejects broader send_message capabilities before tool
// execution, including hidden fields, multiple targets and session overrides.
func IsSpecificReply(args string) bool {
	var payload struct {
		Messages []struct {
			Type      string `json:"type"`
			MessageID string `json:"message_id,omitempty"`
			Text      string `json:"text,omitempty"`
		} `json:"messages"`
	}
	decoder := json.NewDecoder(strings.NewReader(args))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || len(payload.Messages) < 2 {
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return false
	}
	for i, message := range payload.Messages {
		if i == 0 {
			if message.Type != "mention_specific" || strings.TrimSpace(message.MessageID) == "" || message.Text != "" {
				return false
			}
		} else if message.Type != "plain" || strings.TrimSpace(message.Text) == "" || message.MessageID != "" {
			return false
		}
	}
	return true
}
