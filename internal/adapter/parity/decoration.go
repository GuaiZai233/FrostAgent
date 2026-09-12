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

// IsStickerType reports whether the segment or message type represents a sticker / meme.
func IsStickerType(msgType string) bool {
	t := strings.ToLower(strings.TrimSpace(msgType))
	return t == "mface" || t == "sticker"
}

// IsQuoteType reports whether the segment or message type represents a quote or reply.
func IsQuoteType(msgType string) bool {
	t := strings.ToLower(strings.TrimSpace(msgType))
	return t == "quote" || t == "reply"
}

// IsMentionType reports whether the segment or message type represents a user mention or at.
func IsMentionType(msgType string) bool {
	t := strings.ToLower(strings.TrimSpace(msgType))
	return t == "at" || t == "mention_user"
}

// DecorationPlan represents the determined decoration action for an outbound group reply.
type DecorationPlan struct {
	ShouldQuote   bool
	QuoteID       string
	ShouldMention bool
	MentionUserID string
}

// PlanGroupDecoration computes whether quote and mention decorations should be applied
// according to shared parity rules:
// 1. If not a group message or if the message contains a sticker/meme, no decoration is applied.
// 2. If reply is enabled, replyMessageID is non-empty, and the message does not already contain a quote, ShouldQuote is true.
// 3. If mention is enabled, targetUserID is non-empty, and the message does not already mention targetUserID, ShouldMention is true.
// 4. Decoration ordering is always Quote first, then Mention, then original message content.
func PlanGroupDecoration(
	isGroup bool,
	hasSticker bool,
	enableReply bool,
	enableAt bool,
	replyMessageID string,
	targetUserID string,
	hasQuote bool,
	hasMention bool,
) DecorationPlan {
	if !isGroup || hasSticker {
		return DecorationPlan{}
	}
	replyMessageID = strings.TrimSpace(replyMessageID)
	targetUserID = strings.TrimSpace(targetUserID)
	return DecorationPlan{
		ShouldQuote:   enableReply && replyMessageID != "" && !hasQuote,
		QuoteID:       replyMessageID,
		ShouldMention: enableAt && targetUserID != "" && !hasMention,
		MentionUserID: targetUserID,
	}
}

// ContainsStickerOneBot reports whether a sequence of OneBot segments contains a sticker or meme.
func ContainsStickerOneBot(segments []tools.OneBotSegment) bool {
	for _, seg := range segments {
		if IsStickerType(seg.Type) {
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
		if IsQuoteType(seg.Type) {
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
		if !IsMentionType(seg.Type) {
			continue
		}
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
// delegating decoration decision to PlanGroupDecoration.
func WrapGroupReplyOneBot(base []tools.OneBotSegment, messageID int64, userID int64, enableReply bool, enableAt bool) []tools.OneBotSegment {
	hasSticker := ContainsStickerOneBot(base)
	hasQuote := HasQuoteOneBot(base)
	var userIDStr string
	if userID != 0 {
		userIDStr = strconv.FormatInt(userID, 10)
	}
	hasMention := HasMentionOneBot(base, userIDStr)
	var messageIDStr string
	if messageID != 0 {
		messageIDStr = strconv.FormatInt(messageID, 10)
	}

	plan := PlanGroupDecoration(true, hasSticker, enableReply, enableAt, messageIDStr, userIDStr, hasQuote, hasMention)
	if !plan.ShouldQuote && !plan.ShouldMention {
		return base
	}

	// Canonical decoration ordering: Quote first, then Mention, then original message content.
	var quoteSegs []tools.OneBotSegment
	if plan.ShouldQuote {
		quoteSegs = append(quoteSegs, tools.OneBotSegment{
			Type: "reply",
			Data: map[string]any{"id": plan.QuoteID},
		})
	}

	var mentionSegs []tools.OneBotSegment
	if plan.ShouldMention {
		mentionSegs = append(mentionSegs, tools.OneBotSegment{
			Type: "at",
			Data: map[string]any{"qq": plan.MentionUserID},
		})
	}

	if plan.ShouldQuote {
		out := make([]tools.OneBotSegment, 0, len(base)+len(quoteSegs)+len(mentionSegs))
		out = append(out, quoteSegs...)
		out = append(out, mentionSegs...)
		return append(out, base...)
	}

	// When quote is already present in base and we only need to add mention,
	// insert mention after any leading quote segments to preserve [Quote, Mention, Content] ordering.
	insertAt := 0
	for insertAt < len(base) && IsQuoteType(base[insertAt].Type) {
		insertAt++
	}

	out := make([]tools.OneBotSegment, 0, len(base)+len(mentionSegs))
	out = append(out, base[:insertAt]...)
	out = append(out, mentionSegs...)
	out = append(out, base[insertAt:]...)
	return out
}
