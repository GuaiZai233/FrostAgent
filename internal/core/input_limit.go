package core

import (
	"strings"
	"unicode/utf8"
)

// LimitInput uses a conservative UTF-8 byte budget because tokenizers differ
// between compatible providers. It never cuts a rune or changes stored text.
// Zero leaves input untouched so an upstream length rejection can be handled.
func LimitInput(text string, maxTokens int) string {
	if maxTokens <= 0 || len(text) <= maxTokens {
		return text
	}
	end := maxTokens
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return strings.TrimSpace(text[:end])
}
