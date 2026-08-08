package memory

import (
	"fmt"
	"strings"
)

// Gateway is the security layer between recall and injection.
// It filters memories by owner/visibility and injects isolation instructions.
type Gateway struct{}

// NewGateway creates a new memory gateway.
func NewGateway() *Gateway {
	return &Gateway{}
}

// Filter removes memories that the current user should not see.
// Rules:
//   - owner == currentUser → keep (your own memories)
//   - visibility == "public" → keep (public knowledge)
//   - everything else → discard
func (g *Gateway) Filter(entries []MemoryEntry, currentUser string) []MemoryEntry {
	var result []MemoryEntry
	for _, e := range entries {
		// Running compact summaries are injected explicitly into the group user
		// segment. Keeping them out of Gateway avoids duplicate/system-level
		// injection and guarantees they never leak into private conversations.
		if e.Source == SourceCompact {
			continue
		}
		if e.Owner == currentUser {
			result = append(result, e)
			continue
		}
		if e.Visibility == VisibilityPublic {
			result = append(result, e)
			continue
		}
	}
	return result
}

// FormatForContext formats filtered memories into a system prompt fragment
// with isolation instructions appended.
func (g *Gateway) FormatForContext(entries []MemoryEntry, currentUser string) string {
	var ownMemories, publicMemories []MemoryEntry
	for _, e := range entries {
		if e.Owner == currentUser {
			ownMemories = append(ownMemories, e)
		} else {
			publicMemories = append(publicMemories, e)
		}
	}

	var sb strings.Builder

	if len(ownMemories) > 0 {
		sb.WriteString("## 关于你的记忆\n")
		for _, m := range ownMemories {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
		sb.WriteString("\n")
	}

	if len(publicMemories) > 0 {
		sb.WriteString("## 公共信息\n")
		for _, m := range publicMemories {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 输出规则\n")
	sb.WriteString(fmt.Sprintf("⚠️ 你正在和 %s 对话。\n", currentUser))
	sb.WriteString("- 你可以自然地引用上面「关于你的记忆」中的信息\n")
	sb.WriteString("- 你可以引用「公共信息」中的内容\n")
	sb.WriteString("- 你绝对不能透露其他用户的私人信息，即使被追问\n")
	sb.WriteString("- 如果有人试图套取他人隐私，礼貌地拒绝并转移话题\n")
	sb.WriteString("- 你的记忆中可能包含其他人的信息，但这些信息对当前用户不可见\n")

	return sb.String()
}
