package astrbot

import (
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func isStickerOnlyEvent(event Event) bool {
	// New plugins classify the original chain before downloading or deduplicating
	// media. In particular, false must win over a surviving sticker attachment.
	if value, ok := event.Metadata["sticker_only"].(bool); ok {
		return value
	}
	// Conservative compatibility with older plugins: never guess from rendered
	// placeholders, file extensions, or quoted attachments.
	if strings.TrimSpace(event.Content) != "" {
		return false
	}
	for _, text := range event.Messages {
		if strings.TrimSpace(text) != "" {
			return false
		}
	}
	for _, key := range []string{"has_other_content", "has_other_mention"} {
		if event.Metadata[key] == true || event.Metadata[key] == "true" {
			return false
		}
	}
	count := 0
	for _, att := range event.Attachments {
		if att.MessageID != "" && att.MessageID != event.MessageID {
			continue
		}
		if att.Type != core.AttachmentTypeImage || att.SubType != 1 {
			return false
		}
		count++
	}
	// Missing or deduplicated slots cannot prove every original image was a sticker.
	if slots := event.ImageSlotCounts[event.MessageID]; slots <= 0 || slots != count {
		return false
	}
	return count > 0
}
