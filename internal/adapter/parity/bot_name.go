package parity

import (
	"strings"
	"unicode"
)

// ContainsBotName matches literal names within one text segment. Matching
// ignores ASCII letter case and Unicode spaces (including full-width spaces) on
// both sides. Purely ASCII alphanumeric names only match at ASCII word
// boundaries, so short aliases such as "Bot" do not wake on "AstrBot" or
// "chatbot". Punctuation, tabs and line breaks are always literal.
func ContainsBotName(text string, names []string) bool {
	var normalizedText string
	textNormalized := false
	for _, name := range names {
		normalizedName := normalizeBotName(name)
		if normalizedName == "" {
			continue
		}
		if !textNormalized {
			normalizedText = normalizeBotName(text)
			textNormalized = true
		}
		if isASCIIAlphanumericName(normalizedName) {
			if containsASCIIWord(text, normalizedName) {
				return true
			}
			continue
		}
		if strings.Contains(normalizedText, normalizedName) {
			return true
		}
	}
	return false
}

func isASCIIAlphanumericName(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func containsASCIIWord(text, word string) bool {
	runes := []rune(text)
	cleaned := make([]rune, 0, len(runes))
	origins := make([]int, 0, len(runes))
	for i, r := range runes {
		if unicode.Is(unicode.Zs, r) {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		cleaned = append(cleaned, r)
		origins = append(origins, i)
	}
	wordRunes := []rune(word)
	if len(wordRunes) == 0 || len(wordRunes) > len(cleaned) {
		return false
	}
	for start := 0; start+len(wordRunes) <= len(cleaned); start++ {
		matched := true
		for i, expected := range wordRunes {
			if cleaned[start+i] != expected {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		origStart := origins[start]
		origEnd := origins[start+len(wordRunes)-1]
		beforeOK := origStart == 0 || !isASCIIWordRune(runes[origStart-1])
		afterOK := origEnd == len(runes)-1 || !isASCIIWordRune(runes[origEnd+1])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

// isASCIIWordRune treats hyphen, underscore and square brackets as part of an
// ASCII word. This prevents "Bot" from matching role tags such as "[assistant]"
// or hyphenated compounds such as "forged-bot".
func isASCIIWordRune(r rune) bool {
	return isASCIILetterOrDigitRune(r) || r == '-' || r == '_' || r == '[' || r == ']'
}

func isASCIILetterOrDigitRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func normalizeBotName(value string) string {
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
