package llm

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"strings"
)

// filterStickerMessages removes sticker components from the shared outbound
// message format. Envelope fields, ordinary text and images are retained.
// An empty result means the entire message chain consisted of stickers.
func filterStickerMessages(content string) (string, bool) {
	var payload struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return content, false
	}
	kept := make([]json.RawMessage, 0, len(payload.Messages))
	for _, raw := range payload.Messages {
		var message struct {
			Type         string          `json:"type"`
			IsSticker    any             `json:"is_sticker"`
			SubType      any             `json:"sub_type"`
			CamelSubType any             `json:"subType"`
			Data         json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &message) == nil {
			var data struct {
				IsSticker    any `json:"is_sticker"`
				SubType      any `json:"sub_type"`
				CamelSubType any `json:"subType"`
			}
			// Optional or malformed nested data must not hide top-level markers.
			_ = json.Unmarshal(message.Data, &data)
			isSticker, _ := message.IsSticker.(bool)
			dataSticker, _ := data.IsSticker.(bool)
			if sticker.IsMessage(message.Type, isSticker || dataSticker,
				message.SubType, message.CamelSubType, data.SubType, data.CamelSubType) {
				continue
			}
		}
		kept = append(kept, raw)
	}
	if len(kept) == len(payload.Messages) {
		return content, false
	}
	if len(kept) == 0 {
		return "", true
	}
	// Preserve session and unknown envelope values as raw JSON, including numbers
	// that cannot be represented exactly as float64.
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &envelope) != nil {
		return content, false
	}
	filteredMessages, _ := json.Marshal(kept)
	for key := range envelope {
		// Match the struct decoder used above and by the adapters. Replace every
		// alias so re-encoding cannot change which message chain is delivered.
		if strings.EqualFold(key, "messages") {
			envelope[key] = filteredMessages
		}
	}
	filtered, _ := json.Marshal(envelope)
	return string(filtered), true
}
