package parity

import (
	"FrostAgent/internal/tools"
	"encoding/json"
	"strconv"
	"strings"
)

// IsStickerSubType reports whether sub_type or subType equals 1 (typical for stickers/market faces).
func IsStickerSubType(value any) bool {
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

// ContainsStickerOneBot reports whether a sequence of OneBot segments contains a sticker or meme.
func ContainsStickerOneBot(segments []tools.OneBotSegment) bool {
	for _, seg := range segments {
		if seg.Type == "mface" || seg.Type == "sticker" {
			return true
		}
		if seg.Type == "image" {
			if IsStickerSubType(seg.Data["sub_type"]) || IsStickerSubType(seg.Data["subType"]) {
				return true
			}
			if isSticker, ok := seg.Data["is_sticker"].(bool); ok && isSticker {
				return true
			}
		}
		if isSticker, ok := seg.Data["is_sticker"].(bool); ok && isSticker {
			return true
		}
	}
	return false
}

// HasQuoteOneBot reports whether the segment list already contains a reply/quote segment.
func HasQuoteOneBot(segments []tools.OneBotSegment) bool {
	for _, seg := range segments {
		if seg.Type == "reply" || seg.Type == "quote" {
			return true
		}
	}
	return false
}

// HasMentionOneBot reports whether the segment list already contains an @ segment for the specified user ID.
func HasMentionOneBot(segments []tools.OneBotSegment, targetUserID string) bool {
	target := strings.TrimSpace(targetUserID)
	if target == "" {
		return false
	}

	for _, seg := range segments {
		switch seg.Type {
		case "at":
			qqVal := seg.Data["qq"]
			var atQQ string
			switch v := qqVal.(type) {
			case string:
				atQQ = strings.TrimSpace(v)
			case float64:
				atQQ = strconv.FormatFloat(v, 'f', -1, 64)
			case int:
				atQQ = strconv.Itoa(v)
			case int64:
				atQQ = strconv.FormatInt(v, 10)
			case json.Number:
				atQQ = v.String()
			}
			if atQQ == target {
				return true
			}
		case "mention_user":
			if mentionID, ok := seg.Data["mention_user_id"].(string); ok && strings.TrimSpace(mentionID) == target {
				return true
			}
		}
	}
	return false
}

// WrapGroupReplyOneBot applies automatic reply and at decoration to OneBot group messages,
// enforcing strict semantic parity with AstrBot:
// 1. If base contains any sticker, no automatic reply or at is added.
// 2. If base already contains an explicit quote/reply, no duplicate quote is added.
// 3. If base already contains an explicit mention for the user, no duplicate at is added.
// 4. Decoration order: reply first, then at, then base.
func WrapGroupReplyOneBot(base []tools.OneBotSegment, messageID int64, userID int64, enableReply bool, enableAt bool) []tools.OneBotSegment {
	if ContainsStickerOneBot(base) {
		return base
	}

	userIDStr := strconv.FormatInt(userID, 10)
	needsReply := enableReply && messageID != 0 && !HasQuoteOneBot(base)
	needsAt := enableAt && userID != 0 && !HasMentionOneBot(base, userIDStr)

	if !needsReply && !needsAt {
		return base
	}

	out := make([]tools.OneBotSegment, 0, len(base)+2)
	if needsReply {
		out = append(out, tools.OneBotSegment{
			Type: "reply",
			Data: map[string]any{"id": strconv.FormatInt(messageID, 10)},
		})
	}
	if needsAt {
		out = append(out, tools.OneBotSegment{
			Type: "at",
			Data: map[string]any{"qq": userIDStr},
		})
	}
	return append(out, base...)
}
