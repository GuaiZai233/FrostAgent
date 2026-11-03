package parity

import (
	"strings"
	"unicode"
)

// ContainsBotName matches literal names within one text segment. Names containing
// English letters ignore ASCII letter case and Unicode spaces on both sides.
// Names without English letters retain exact matching; punctuation, tabs and
// line breaks are always literal.
func ContainsBotName(text string, names []string) bool {
	var normalizedText string
	normalized := false
	for _, name := range names {
		if name == "" {
			continue
		}
		if !strings.ContainsAny(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			if strings.Contains(text, name) {
				return true
			}
			continue
		}
		if !normalized {
			normalizedText = normalizeEnglishBotName(text)
			normalized = true
		}
		if strings.Contains(normalizedText, normalizeEnglishBotName(name)) {
			return true
		}
	}
	return false
}

func normalizeEnglishBotName(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case unicode.Is(unicode.Zs, r):
			return -1
		default:
			return r
		}
	}, value)
}
