package llm

import "encoding/json"

// filterStickerImages removes only sticker images from the shared outbound
// message format. Ordinary text and images retain their fields and order.
// An empty result means the entire message chain consisted of stickers.
func filterStickerImages(content string) (string, bool) {
	var payload struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return content, false
	}
	kept := make([]json.RawMessage, 0, len(payload.Messages))
	for _, raw := range payload.Messages {
		var message struct {
			Type      string `json:"type"`
			IsSticker bool   `json:"is_sticker"`
		}
		if json.Unmarshal(raw, &message) == nil && message.Type == "image" && message.IsSticker {
			continue
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
