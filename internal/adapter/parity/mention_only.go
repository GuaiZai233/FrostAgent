package parity

import (
	"FrostAgent/internal/tools"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// MentionOnlyGuidance defines the standardized prompt guidance injected when
// a group interaction consists solely of an @Bot mention without text or image attachments.
const MentionOnlyGuidance = "This is an explicit mention-only invitation to respond. Infer the relevant preceding discussion from group_running_summary and recent_group_messages; if no useful context exists, acknowledge naturally and ask what the sender needs."

var mentionTokenRegex = regexp.MustCompile(`\[@\d+\]`)

// StripBotSelfMentionTokens strips only the bot's own [@<selfID>] token from the text.
// Mentions of other users (e.g. [@<otherQQ>]) are preserved.
func StripBotSelfMentionTokens(text string, selfID int64) string {
	if selfID == 0 {
		return text
	}
	pattern := `\[@` + strconv.FormatInt(selfID, 10) + `\]`
	re := regexp.MustCompile(pattern)
	return strings.TrimSpace(re.ReplaceAllString(text, ""))
}

// StripBotMentionTokens strips [@<qq>] tokens formatted by OneBot extractUserText.
// Deprecated: prefer StripBotSelfMentionTokens to avoid stripping mentions of other users.
func StripBotMentionTokens(text string) string {
	return strings.TrimSpace(mentionTokenRegex.ReplaceAllString(text, ""))
}

// MentionInteraction represents the canonical interaction attributes evaluated
// across adapters to determine if an inbound group message is an explicit mention-only invitation.
type MentionInteraction struct {
	IsGroup         bool
	IsMentionedBot  bool
	HasOtherMention bool
	HasReply        bool
	HasImages       bool
	HasOtherContent bool
	UserText        string
}

// IsMentionOnly evaluates whether a canonical interaction is an explicit mention-only interaction:
// 1. Must be a group message (IsGroup == true).
// 2. Must explicitly target/mention the bot (IsMentionedBot == true).
// 3. Must NOT mention any other user (HasOtherMention == false).
// 4. Must NOT have reply/quote context (HasReply == false).
// 5. Must NOT contain images or media attachments (HasImages == false).
// 6. Must NOT contain non-text/non-image components like Face, Record, Video (HasOtherContent == false).
// 7. Must NOT contain non-whitespace text (strings.TrimSpace(UserText) == "").
func IsMentionOnly(interaction MentionInteraction) bool {
	if !interaction.IsGroup || !interaction.IsMentionedBot {
		return false
	}
	if interaction.HasOtherMention || interaction.HasReply || interaction.HasImages || interaction.HasOtherContent {
		return false
	}
	return strings.TrimSpace(interaction.UserText) == ""
}

// IsMentionOnlyOneBot reports whether a OneBot group message is a mention-only interaction:
// group message, explicitly wakes/mentions the bot, has empty text (or text consisting solely
// of formatted [@<selfID>] tokens and whitespace), no images, and no reply context.
// Mentions of other users in userText will prevent this from returning true.
func IsMentionOnlyOneBot(isGroup bool, selfID int64, mentionedBot bool, userText string, hasImages bool, hasReply bool) bool {
	stripped := StripBotSelfMentionTokens(userText, selfID)
	hasOtherMention := mentionTokenRegex.MatchString(stripped)
	return IsMentionOnly(MentionInteraction{
		IsGroup:         isGroup,
		IsMentionedBot:  mentionedBot,
		HasOtherMention: hasOtherMention,
		HasReply:        hasReply,
		HasImages:       hasImages,
		HasOtherContent: false,
		UserText:        stripped,
	})
}

// IsMentionOnlyOneBotSegments checks whether raw OneBot message segments constitute
// a pure @Bot mention-only interaction:
// 1. isGroup is true.
// 2. hasImages is false.
// 3. hasReply is false.
// 4. At least one "at" segment explicitly targets the bot's self_id.
// 5. All other segments in the message chain are whitespace-only "text" segments
//    (no non-empty text, no media, no face, no replies, and no mentions of other users).
func IsMentionOnlyOneBotSegments(isGroup bool, selfID int64, segments []tools.OneBotSegment, hasImages bool, hasReply bool) bool {
	if !isGroup || hasImages || hasReply || len(segments) == 0 {
		return false
	}

	selfIDStr := strconv.FormatInt(selfID, 10)
	hasBotMention := false
	hasOtherMention := false
	hasOtherContent := false
	var textBuilder strings.Builder

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
			if atQQ == selfIDStr {
				hasBotMention = true
			} else {
				hasOtherMention = true
			}
		case "text":
			text, ok := seg.Data["text"].(string)
			if ok {
				textBuilder.WriteString(text)
			}
		default:
			hasOtherContent = true
		}
	}

	return IsMentionOnly(MentionInteraction{
		IsGroup:         isGroup,
		IsMentionedBot:  hasBotMention,
		HasOtherMention: hasOtherMention,
		HasReply:        hasReply,
		HasImages:       hasImages,
		HasOtherContent: hasOtherContent,
		UserText:        textBuilder.String(),
	})
}

// IsMentionOnlyAstrBot reports whether an AstrBot event is a mention-only interaction:
// group message, at bot, empty text, no attachments or media structures, no reply context,
// no other mentions, and no non-text components.
// When hasReply is true (e.g. metadata carries reply_message_id), mention-only is false
// to maintain semantic parity with OneBot's reply segment detection.
func IsMentionOnlyAstrBot(
	isGroup bool,
	isAt bool,
	content string,
	hasImages bool,
	hasReply bool,
	hasOtherMention bool,
	hasOtherContent bool,
) bool {
	return IsMentionOnly(MentionInteraction{
		IsGroup:         isGroup,
		IsMentionedBot:  isAt,
		HasOtherMention: hasOtherMention,
		HasReply:        hasReply,
		HasImages:       hasImages,
		HasOtherContent: hasOtherContent,
		UserText:        content,
	})
}
