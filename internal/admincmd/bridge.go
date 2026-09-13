package admincmd

import (
	"strings"
)

// StripLeadingMention removes a leading @mention or [@mention] component from text,
// returning the trimmed remainder of the message.
func StripLeadingMention(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[@") {
		if _, after, found := strings.Cut(text, "]"); found {
			return strings.TrimSpace(after)
		}
	}
	if strings.HasPrefix(text, "@") {
		// Find first whitespace separator
		idx := strings.IndexAny(text, " \t\r\n　")
		if idx != -1 {
			return strings.TrimSpace(text[idx+1:])
		}
	}
	return text
}
