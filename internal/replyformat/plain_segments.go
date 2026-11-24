package replyformat

import "fmt"

// PlainGroupIDs assigns each component of an ordered batch to a logical segment.
// A plain component starts a new segment only when the current segment already
// holds a plain one; every other component stays in the segment it appears in.
// Components that must be sent on their own (files, for example) do not reset
// the state, so an image after them still belongs to the segment before it.
func PlainGroupIDs[T any](items []T, kind func(T) string) []int {
	ids := make([]int, len(items))
	group, plains := 0, 0
	for i, item := range items {
		if kind(item) == "plain" {
			if plains > 0 {
				group++
				plains = 0
			}
			plains++
		}
		ids[i] = group
	}
	return ids
}

// GroupPlainSegments splits an ordered component list into logical segments
// using PlainGroupIDs.
func GroupPlainSegments[T any](items []T, kind func(T) string) [][]T {
	ids := PlainGroupIDs(items, kind)
	var groups [][]T
	for i, item := range items {
		if i == 0 || ids[i] != ids[i-1] {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], item)
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
