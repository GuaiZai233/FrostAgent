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

// FindEnclosingClause finds the punctuation-delimited clause in fullText that encloses sub.
func FindEnclosingClause(fullText, sub string) string {
	if sub == "" || fullText == "" {
		return ""
	}
	subLower := strings.ToLower(strings.TrimSpace(sub))

	delims := func(r rune) bool {
		switch r {
		case '，', ',', '。', '.', '！', '!', '？', '?', '；', ';', '\n', '\r', '、', '\t':
			return true
		default:
			return false
		}
	}

	clauses := strings.FieldsFunc(fullText, delims)
	for _, clause := range clauses {
		cLower := strings.ToLower(clause)
		if strings.Contains(cLower, subLower) {
			return strings.TrimSpace(clause)
		}
	}

	subTokens := ExtractSubstantiveTokens(subLower)
	if len(subTokens) > 0 {
		for _, token := range subTokens {
			tokenLower := strings.ToLower(token)
			for _, clause := range clauses {
				if strings.Contains(strings.ToLower(clause), tokenLower) {
					return strings.TrimSpace(clause)
				}
			}
		}
	}
	return strings.TrimSpace(fullText)
}

func isAsciiWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// CountNegationMarkers counts the occurrence of negation tokens in s,
// ignoring common non-negating conjunctions like "不仅", "不但", "不管", "不过".
func CountNegationMarkers(s string) int {
	if s == "" {
		return 0
	}
	sLower := strings.ToLower(s)
	// Strip conjunctions that contain '不' but do not negate predicates
	conjunctions := []string{"不仅", "不但", "不管", "不过", "不论", "不料", "不单", "不顾"}
	for _, c := range conjunctions {
		sLower = strings.ReplaceAll(sLower, c, " ")
	}

	count := 0
	negationTokens := []string{
		"不是", "不会", "不能", "不知", "不要", "不想", "不喜欢", "没有", "未曾", "无法", "并非", "毫无", "绝不", "决不", "从不",
		"不", "没", "未", "无", "非", "莫", "别",
		"cannot", "can't", "won't", "don't", "didn't", "isn't", "aren't", "wasn't", "weren't", "doesn't", "haven't", "hasn't", "hadn't", "wouldn't", "couldn't", "shouldn't",
		"never", "neither", "nor", "none", "without", "not", "no", "n't",
	}

	for _, neg := range negationTokens {
		searchStart := 0
		isAscii := true
		for i := 0; i < len(neg); i++ {
			if neg[i] >= 128 {
				isAscii = false
				break
			}
		}
		for {
			idx := strings.Index(sLower[searchStart:], neg)
			if idx == -1 {
				break
			}
			realIdx := searchStart + idx
			if isAscii {
				// Ensure word boundary for ASCII tokens so "not" doesn't match inside "noted" or "notice"
				boundaryBefore := realIdx == 0 || !isAsciiWordChar(sLower[realIdx-1])
				boundaryAfter := realIdx+len(neg) >= len(sLower) || !isAsciiWordChar(sLower[realIdx+len(neg)])
				if !boundaryBefore || !boundaryAfter {
					searchStart = realIdx + len(neg)
					continue
				}
			}
			count++
			sLower = sLower[:realIdx] + " " + sLower[realIdx+len(neg):]
			searchStart = realIdx + 1
		}
	}
	return count
}

// HasPolarityInversion checks whether the negation polarity between the source message / evidence
// and the generated content has been inverted (e.g. "我不是管理员" -> "我是管理员", "我不喜欢舞萌" -> "我喜欢舞萌").
func HasPolarityInversion(evidence, content, srcMsgContent string) bool {
	contentNeg := CountNegationMarkers(content)
	evNeg := CountNegationMarkers(evidence)

	enclosingClause := FindEnclosingClause(srcMsgContent, evidence)
	clauseNeg := CountNegationMarkers(enclosingClause)

	// If the source (evidence or its enclosing clause) is negated, but content dropped all negation
	if (evNeg > 0 || clauseNeg > 0) && contentNeg == 0 {
		return true
	}

	// If the source is not negated, but content introduced negation
	if evNeg == 0 && clauseNeg == 0 && contentNeg > 0 {
		return true
	}

	return false
}

// HasUnsupportedAdditions checks whether content contains substantive predicates, suffixes,
// or facts not supported by the source message, or inverts negation polarity.
// It strips allowed generic framing phrases (such as "用户", "自述", "发言人") and the sender name,
// then verifies that all remaining substantive tokens appear in the source message.
func HasUnsupportedAdditions(content, srcMsgContent, senderName string) bool {
	srcLower := strings.ToLower(srcMsgContent)
	if srcLower == "" {
		return true
	}

	// Guard against polarity inversion (e.g. negation dropped or added)
	if HasPolarityInversion(content, content, srcMsgContent) {
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

func hasFirstPersonMarker(s string) bool {
	sLower := strings.ToLower(s)
	markers := []string{"我", "俺", "咱", "自己", "本人", "在下", "吾"}
	for _, m := range markers {
		if strings.Contains(sLower, m) {
			return true
		}
	}
	engMarkers := []string{"i ", "i'm", "i've", "i'll", "i'd", "my ", "mine", "me ", "myself"}
	for _, m := range engMarkers {
		if strings.HasPrefix(sLower, m) || strings.Contains(sLower, " "+m) {
			return true
		}
	}
	return false
}

// IsFirstPersonStatement determines whether the quoted evidence and content represent
// a genuine first-person self-attribution statement by the speaker, rather than an unrelated
// first-person marker elsewhere in the message or a claim about a third party.
func IsFirstPersonStatement(evidence, content, fullContent, senderName string) bool {
	evTrim := strings.TrimSpace(evidence)
	if evTrim == "" {
		return false
	}

	contentTrim := strings.TrimSpace(content)
	cLower := strings.ToLower(contentTrim)
	sName := strings.ToLower(strings.TrimSpace(senderName))

	// 1. Check if content explicitly attributes the statement to someone other than the sender
	selfFraming := []string{"用户", "群友", "本人", "自己", "发言人", "成员", "我", "俺", "咱", "user", "speaker", "member"}
	hasSelfPrefix := false
	for _, f := range selfFraming {
		if strings.HasPrefix(cLower, strings.ToLower(f)) {
			hasSelfPrefix = true
			break
		}
	}
	if sName != "" && strings.HasPrefix(cLower, sName) {
		hasSelfPrefix = true
	}

	// 2. Find the specific clause in fullContent that encloses evidence
	enclosingClause := FindEnclosingClause(fullContent, evTrim)
	clauseLower := strings.ToLower(enclosingClause)
	evLower := strings.ToLower(evTrim)

	// 3. The evidence or its enclosing clause must contain a first-person marker or start with senderName
	hasSelfMarker := hasFirstPersonMarker(evLower) || hasFirstPersonMarker(clauseLower)
	hasSenderName := false
	if sName != "" {
		if strings.HasPrefix(evLower, sName) || strings.HasPrefix(clauseLower, sName) {
			hasSenderName = true
		}
	}

	if !hasSelfMarker && !hasSenderName {
		// Neither the quoted evidence nor the enclosing clause has any first-person indicator or speaker name!
		return false
	}

	// 4. If content explicitly attributes the statement to a third party (does not start with senderName
	// and does not start with an allowed self-framing prefix), and evidence has no first-person marker:
	if !hasSelfPrefix && !hasFirstPersonMarker(evLower) {
		return false
	}

	return true
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
