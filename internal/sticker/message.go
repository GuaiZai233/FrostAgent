package sticker

import (
	"encoding/json"
	"strings"
)

// IsMessageType reports whether a segment type identifies a sticker or meme.
func IsMessageType(msgType string) bool {
	t := strings.ToLower(strings.TrimSpace(msgType))
	return t == "mface" || t == "sticker"
}

// IsImageSubType recognizes the sticker subtype used by supported adapters.
func IsImageSubType(value any) bool {
	switch v := value.(type) {
	case int:
		return v == 1
	case int64:
		return v == 1
	case float64:
		return v == 1
	case string:
		return strings.TrimSpace(v) == "1"
	case json.Number:
		return v.String() == "1"
	default:
		return false
	}
}

// IsMessage applies the same classification to outbound message components
// and OneBot segments. A positive marker cannot be overridden by another
// negative marker; subtype markers only classify image components.
func IsMessage(msgType string, isSticker bool, subTypes ...any) bool {
	if isSticker || IsMessageType(msgType) {
		return true
	}
	if msgType == "image" {
		for _, subType := range subTypes {
			if IsImageSubType(subType) {
				return true
			}
		}
	}
	return false
}
