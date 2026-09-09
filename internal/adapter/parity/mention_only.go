package parity

import "strings"

// MentionOnlyGuidance defines the standardized prompt guidance injected when
// a group interaction consists solely of an @Bot mention without text or image attachments.
const MentionOnlyGuidance = "This is an explicit mention-only invitation to respond. Infer the relevant preceding discussion from group_running_summary and recent_group_messages; if no useful context exists, acknowledge naturally and ask what the sender needs."

// IsMentionOnlyOneBot reports whether a OneBot group message is a mention-only interaction:
// group message, explicitly wakes/mentions the bot, has empty text and no images.
func IsMentionOnlyOneBot(isGroup bool, mentionedBot bool, userText string, hasImages bool) bool {
	return isGroup && mentionedBot && strings.TrimSpace(userText) == "" && !hasImages
}

// IsMentionOnlyAstrBot reports whether an AstrBot event is a mention-only interaction.
func IsMentionOnlyAstrBot(isGroup bool, isAt bool, content string, attachmentCount int) bool {
	return isGroup && isAt && strings.TrimSpace(content) == "" && attachmentCount == 0
}
