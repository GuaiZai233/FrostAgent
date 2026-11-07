package onebot

import (
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

// isStickerOnlyEvent examines current components, never quoted message content.
// Unknown components and ordinary images keep the normal reply path available.
func isStickerOnlyEvent(event model.OneBotEvent) bool {
	found := false
	for _, raw := range EventRawMessages(event) {
		segments := ParseMessageSegments(raw)
		if len(segments) == 0 {
			return false
		}
		for _, segment := range segments {
			switch segment.Type {
			case "text":
				text, ok := segment.Data["text"].(string)
				if !ok || strings.TrimSpace(text) != "" {
					return false
				}
			case "reply":
				// A quote marker is not the content being reacted to.
			case "mface", "sticker":
				found = true
			case "image":
				flag, _ := segment.Data["is_sticker"].(bool)
				if !sticker.IsMessage(segment.Type, flag, segment.Data["sub_type"], segment.Data["subType"]) &&
					!content.IsMarketFaceSegment(segment) {
					return false
				}
				found = true
			default:
				return false
			}
		}
	}
	return found
}
