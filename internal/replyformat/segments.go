// Package replyformat interprets the model's ordinary reply delivery markers.
package replyformat

import (
	"math/rand"
	"sort"
	"strconv"
	"strings"
)

const EnvKey = "ENABLE_MULTI_SEGMENT_REPLY"
const SegmentMark = "<FROSTAGENT_SEGMENT_MARK>"

// LimitEnvKey caps how many segments of one reply are delivered. Zero, empty,
// or invalid values mean no cap. One disables segmentation.
const LimitEnvKey = "MULTI_SEGMENT_REPLY_MAX_SEGMENTS"

// MaxSegmentLimit is the largest accepted value for LimitEnvKey.
const MaxSegmentLimit = 20

// ParseLimit converts the LimitEnvKey value into a segment cap. Zero means no cap.
func ParseLimit(value string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || limit < 1 || limit > MaxSegmentLimit {
		return 0
	}
	return limit
}

// Enabled reads the live settings, treating a one-segment limit as disabled.
func Enabled(getenv func(string) string) bool {
	return getenv(EnvKey) == "true" && ParseLimit(getenv(LimitEnvKey)) != 1
}

// SplitLimited splits text for delivery. When segmentation is enabled and limit
// is positive, it keeps a random subset of at most limit segments, preserving
// their original order and discarding the rest. A limit of one disables splitting.
func SplitLimited(text string, enabled bool, limit int) []string {
	if limit == 1 {
		enabled = false
	}
	parts := Split(text, enabled)
	if !enabled || limit <= 0 || len(parts) <= limit {
		return parts
	}
	keep := rand.Perm(len(parts))[:limit]
	sort.Ints(keep)
	kept := make([]string, 0, limit)
	for _, index := range keep {
		kept = append(kept, parts[index])
	}
	return kept
}

// Split preserves single newlines. Disabled segmentation only removes markers;
// ordinary text and whitespace remain unchanged.
func Split(text string, enabled bool) []string {
	var parts []string
	var current strings.Builder
	split := false
	flush := func() {
		if part := strings.TrimSpace(current.String()); part != "" {
			parts = append(parts, part)
		}
		current.Reset()
	}
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], SegmentMark) {
			i += len(SegmentMark)
			if enabled {
				split = true
				flush()
			}
			continue
		}
		n := 0
		if enabled {
			if strings.HasPrefix(text[i:], "\r\n\r\n") {
				n = 4
			} else if strings.HasPrefix(text[i:], "\n\n") {
				n = 2
			}
		}
		if n > 0 {
			split = true
			flush()
			i += n
			continue
		}
		current.WriteByte(text[i])
		i++
	}
	if !split {
		if strings.TrimSpace(current.String()) == "" {
			return nil
		}
		return []string{current.String()}
	}
	flush()
	return parts
}

// WithReceipt attaches backend-generated billing text only to the last segment.
// Receipts are never parsed as model output or split on their own blank lines.
func WithReceipt(parts []string, receipt string) []string {
	result := append([]string(nil), parts...)
	if receipt == "" {
		return result
	}
	if len(result) == 0 {
		return []string{receipt}
	}
	result[len(result)-1] += "\n\n" + receipt
	return result
}
