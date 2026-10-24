package llm

import (
	"FrostAgent/internal/sticker"
	"encoding/json"
)

// filterStickerMessages removes sticker components from the shared outbound
// message format. Ordinary text and images retain their fields and order.
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
	// Delivery consumes only the messages envelope; raw components preserve
	// media sources and any platform-specific fields on retained messages.
	payload.Messages = kept
	filtered, _ := json.Marshal(payload)
	return string(filtered), true
}
