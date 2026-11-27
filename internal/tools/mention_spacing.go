package tools

import "strings"

// SeparateMentionText makes text that follows an @ segment start with exactly one
// space. Leading spaces and tabs are normalized; text that starts with a line
// break (or is blank) is left untouched.
func SeparateMentionText(text string) string {
	trimmed := strings.TrimLeftFunc(text, func(r rune) bool {
		return r != '\n' && r != '\r' && isSpaceRune(r)
	})
	if trimmed == "" || trimmed[0] == '\n' || trimmed[0] == '\r' {
		return text
	}
	return " " + trimmed
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '　' || r == ' ' || r == '\v' || r == '\f'
}
