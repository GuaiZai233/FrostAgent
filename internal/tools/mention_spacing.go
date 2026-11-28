package tools

import (
	"strings"
	"unicode"
)

// SeparateMentionText makes text that follows an @ segment start with exactly
// one space. Leading horizontal whitespace is normalized. Text that starts with
// a line break keeps it (without leading spaces before it); blank text is left
// untouched.
func SeparateMentionText(text string) string {
	trimmed := strings.TrimLeftFunc(text, func(r rune) bool {
		return r != '\n' && r != '\r' && unicode.IsSpace(r)
	})
	if trimmed == "" {
		return text
	}
	if trimmed[0] == '\n' || trimmed[0] == '\r' {
		return trimmed
	}
	return " " + trimmed
}
