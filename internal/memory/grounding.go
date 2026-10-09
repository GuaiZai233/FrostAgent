package memory

import (
	"strings"
	"unicode/utf8"
)

const (
	MinEvidenceRunes = 3
	MaxEvidenceRunes = 500
	MaxSummaryRunes  = 500
	MaxTagLength     = 50
	MaxTagCount      = 10
)

// ValidateEvidence verifies that evidence is a non-trivial verbatim substring of srcMsgContent.
// Under Option A, the verbatim quoted excerpt is the authoritative memory content.
// It returns the trimmed evidence and true if it satisfies:
// 1. MinEvidenceRunes (3) <= utf8.RuneCountInString(evidence) <= MaxEvidenceRunes (500)
// 2. strings.Contains(srcMsgContent, evidence)
func ValidateEvidence(srcMsgContent, evidence string) (string, bool) {
	evTrim := strings.TrimSpace(evidence)
	if evTrim == "" {
		return "", false
	}
	rCount := utf8.RuneCountInString(evTrim)
	if rCount < MinEvidenceRunes || rCount > MaxEvidenceRunes {
		return "", false
	}
	if !strings.Contains(srcMsgContent, evTrim) {
		return "", false
	}
	return evTrim, true
}

// SanitizeTags bounds, trims, and deduplicates tags.
func SanitizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var result []string
	for _, t := range tags {
		tTrim := strings.TrimSpace(t)
		if tTrim == "" {
			continue
		}
		if utf8.RuneCountInString(tTrim) > MaxTagLength {
			tTrim = string([]rune(tTrim)[:MaxTagLength])
		}
		if !seen[tTrim] {
			seen[tTrim] = true
			result = append(result, tTrim)
			if len(result) >= MaxTagCount {
				break
			}
		}
	}
	return result
}

// SanitizeSummary trims and bounds the optional non-authoritative display summary.
func SanitizeSummary(summary string) string {
	sTrim := strings.TrimSpace(summary)
	if sTrim == "" {
		return ""
	}
	if utf8.RuneCountInString(sTrim) > MaxSummaryRunes {
		return string([]rune(sTrim)[:MaxSummaryRunes])
	}
	return sTrim
}
