package parity

import "github.com/GuaiZai233/FrostAgent/internal/tools"

// SeparateMentionText normalizes the separator after an @ segment to exactly one space.
func SeparateMentionText(text string) string {
	return tools.SeparateMentionText(text)
}
