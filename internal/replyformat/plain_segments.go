package replyformat

import "fmt"

// GroupPlainSegments splits an ordered component list into delivery segments.
// A new segment starts at a plain component whenever the current segment already
// holds one, so each plain component is sent as its own message. Non-plain
// components (quotes, mentions, media) stay in the segment where they appear.
func GroupPlainSegments[T any](items []T, kind func(T) string) [][]T {
	var groups [][]T
	var current []T
	plains := 0
	for _, item := range items {
		if kind(item) == "plain" {
			if plains > 0 {
				groups = append(groups, current)
				current, plains = nil, 0
			}
			plains++
		}
		current = append(current, item)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// CountPlain returns how many components are plain text.
func CountPlain[T any](items []T, kind func(T) string) int {
	count := 0
	for _, item := range items {
		if kind(item) == "plain" {
			count++
		}
	}
	return count
}

// CheckPlainLimit rejects a batch that exceeds the configured segment cap. Zero
// means no cap. The check runs before anything is sent, so a rejected batch
// delivers nothing and the model can retry with fewer segments.
func CheckPlainLimit(plainCount, limit int) error {
	if limit > 0 && plainCount > limit {
		return fmt.Errorf("本次消息含 %d 个 plain 段，超过上限 %d；请减少分段后重试", plainCount, limit)
	}
	return nil
}
