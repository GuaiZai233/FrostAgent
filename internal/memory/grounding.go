package memory

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ExtractSubstantiveTokens extracts substantive semantic tokens from text:
// - Han characters: each distinct Han rune
// - Alphanumeric words: words with length >= 2 in lowercase
func ExtractSubstantiveTokens(s string) []string {
	var tokens []string
	var currentWord strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			if currentWord.Len() >= 2 {
				tokens = append(tokens, strings.ToLower(currentWord.String()))
			}
			currentWord.Reset()
			tokens = append(tokens, string(r))
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			currentWord.WriteRune(r)
		} else {
			if currentWord.Len() >= 2 {
				tokens = append(tokens, strings.ToLower(currentWord.String()))
			}
			currentWord.Reset()
		}
	}
	if currentWord.Len() >= 2 {
		tokens = append(tokens, strings.ToLower(currentWord.String()))
	}
	return tokens
}

// CountSubstantiveTokenOverlap calculates the token overlap between evidence and content.
func CountSubstantiveTokenOverlap(evidence, content string) (overlap int, totalEvTokens int) {
	evTokens := ExtractSubstantiveTokens(evidence)
	if len(evTokens) == 0 {
		return 0, 0
	}
	contentLower := strings.ToLower(content)
	contentTokens := make(map[string]bool)
	for _, ct := range ExtractSubstantiveTokens(content) {
		contentTokens[ct] = true
	}

	seen := make(map[string]bool)
	for _, token := range evTokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		if contentTokens[token] || strings.Contains(contentLower, token) {
			overlap++
		}
	}
	return overlap, len(seen)
}

// HasUnsupportedAdditions checks whether content contains substantive predicates, suffixes,
// or facts not supported by the source message. It strips allowed generic framing phrases
// (such as "用户", "自述", "发言人") and the sender name, then verifies that all remaining substantive
// tokens appear in the source message.
func HasUnsupportedAdditions(content, srcMsgContent, senderName string) bool {
	srcLower := strings.ToLower(srcMsgContent)
	if srcLower == "" {
		return true
	}

	stripped := strings.ToLower(content)

	// Allowed framing phrases that an LLM distiller may legitimately introduce
	// without introducing new factual claims.
	allowedFraming := []string{
		"用户", "群友", "群成员", "群内", "本人", "自己", "发言人", "成员", "大家",
		"自述", "表示", "提到", "关于", "称", "说",
		"的", "了", "在", "是", "也", "都", "和", "与", "及", "很", "非常", "一个", "这位", "该", "而且", "顺便", "此外", "另外",
		"user", "users", "speaker", "speakers", "member", "members", "group", "groups",
		"stated", "said", "claims", "claimed", "reported", "mentioned", "about",
		"and", "or", "the", "a", "an", "is", "was", "are", "were", "to", "in", "on", "at", "by", "for", "with", "also",
	}

	for _, phrase := range allowedFraming {
		stripped = strings.ReplaceAll(stripped, phrase, " ")
	}

	if senderName != "" {
		sName := strings.ToLower(strings.TrimSpace(senderName))
		if sName != "" {
			stripped = strings.ReplaceAll(stripped, sName, " ")
		}
	}

	remainingTokens := ExtractSubstantiveTokens(stripped)
	for _, token := range remainingTokens {
		tokenLower := strings.ToLower(token)
		if !strings.Contains(srcLower, tokenLower) {
			// Found an unsupported substantive token (new predicate / fabricated suffix)!
			return true
		}
	}
	return false
}

// IsFirstPersonStatement determines whether evidence or the full message indicates
// a genuine first-person self-attribution statement by the speaker.
func IsFirstPersonStatement(evidence, fullContent, senderName string) bool {
	combined := strings.ToLower(evidence + " " + fullContent)
	markers := []string{"我", "俺", "咱", "自己", "本人", "在下", "吾", "i ", "i'm", "i've", "i'll", "i'd", "my ", "mine", "me ", "myself"}
	for _, m := range markers {
		if strings.Contains(combined, m) {
			return true
		}
	}
	if senderName != "" {
		sName := strings.ToLower(strings.TrimSpace(senderName))
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(evidence)), sName) ||
			strings.HasPrefix(strings.ToLower(strings.TrimSpace(fullContent)), sName) {
			return true
		}
	}
	return false
}

// IsCrossSpeakerClaim checks if content begins by attributing claims to another speaker name.
func IsCrossSpeakerClaim(content string, otherSpeakerNames []string) bool {
	contentLower := strings.ToLower(strings.TrimSpace(content))
	for _, otherName := range otherSpeakerNames {
		otherName = strings.ToLower(strings.TrimSpace(otherName))
		if otherName == "" {
			continue
		}
		if strings.HasPrefix(contentLower, otherName) {
			rem := contentLower[len(otherName):]
			if len(rem) == 0 {
				return true
			}
			firstRune, _ := utf8.DecodeRuneInString(rem)
			if unicode.IsSpace(firstRune) || unicode.IsPunct(firstRune) || unicode.Is(unicode.Han, firstRune) {
				return true
			}
		}
	}
	return false
}
