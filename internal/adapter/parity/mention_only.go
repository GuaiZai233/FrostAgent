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

// IsMentionOnlyOneBot reports whether a OneBot group message is a mention-only interaction:
// group message, explicitly wakes/mentions the bot, has empty text (or text consisting solely
// of formatted [@<selfID>] tokens and whitespace) and no images.
// Mentions of other users in userText will prevent this from returning true.
func IsMentionOnlyOneBot(isGroup bool, selfID int64, mentionedBot bool, userText string, hasImages bool) bool {
	if !isGroup || !mentionedBot || hasImages {
		return false
	}
	return strings.TrimSpace(StripBotSelfMentionTokens(userText, selfID)) == ""
}

// IsMentionOnlyOneBotSegments checks whether raw OneBot message segments constitute
// a pure @Bot mention-only interaction:
// 1. isGroup is true.
// 2. hasImages is false.
// 3. At least one "at" segment explicitly targets the bot's self_id.
// 4. All other segments in the message chain are whitespace-only "text" segments
//    (no non-empty text, no media, no face, no replies, and no mentions of other users).
func IsMentionOnlyOneBotSegments(isGroup bool, selfID int64, segments []tools.OneBotSegment, hasImages bool) bool {
	if !isGroup || hasImages || len(segments) == 0 {
		return false
	}

	selfIDStr := strconv.FormatInt(selfID, 10)
	hasBotMention := false

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
				return false
			}
		case "text":
			text, ok := seg.Data["text"].(string)
			if !ok || strings.TrimSpace(text) != "" {
				return false
			}
		default:
			return false
		}
	}

	return hasBotMention
}

// IsMentionOnlyAstrBot reports whether an AstrBot event is a mention-only interaction.
func IsMentionOnlyAstrBot(isGroup bool, isAt bool, content string, attachmentCount int) bool {
	return isGroup && isAt && strings.TrimSpace(content) == "" && attachmentCount == 0
}
