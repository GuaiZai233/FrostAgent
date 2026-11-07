package parity

import (
	"unicode"
	"unicode/utf8"
)

// SeparateMentionText adds a separator after an automatic mention while preserving existing whitespace.
func SeparateMentionText(text string) string {
	if text == "" {
		return text
	}
	first, _ := utf8.DecodeRuneInString(text)
	if unicode.IsSpace(first) {
		return text
	}
	return " " + text
}
