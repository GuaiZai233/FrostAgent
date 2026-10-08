package memory

import (
	"fmt"
	"strings"
)

// Gateway is the security layer between recall and injection.
// It enforces scope boundaries and formats memory context with isolation instructions.
type Gateway struct{}

// NewGateway creates a new memory gateway.
func NewGateway() *Gateway {
	return &Gateway{}
}

// Filter removes memories that the current private user should not see.
// In private chat, access is strictly isolated per QQ user: only memories belonging to currentUser are kept.
// Legacy public visibility and compact source are discarded.
func (g *Gateway) Filter(entries []MemoryEntry, currentUser string) []MemoryEntry {
	return g.FilterPrivate(entries, currentUser)
}

// FilterPrivate filters memories for private chat, keeping only entries strictly owned by currentUser.
func (g *Gateway) FilterPrivate(entries []MemoryEntry, currentUser string) []MemoryEntry {
	var result []MemoryEntry
	for _, e := range entries {
		if e.Source == SourceCompact {
			continue
		}
		if OwnersMatch(e.Owner, currentUser) {
			result = append(result, e)
		}
	}
	return result
}

// FilterGroup filters memories for group chat.
// All entries in a group's store are accessible to members of that group,
// since access isolation is strictly enforced at the group store boundary.
func (g *Gateway) FilterGroup(entries []MemoryEntry) []MemoryEntry {
	var result []MemoryEntry
	for _, e := range entries {
		if e.Source == SourceCompact {
			continue
		}
		result = append(result, e)
	}
	return result
}

// FormatForContext formats filtered memories into a system prompt fragment
// with private isolation instructions.
func (g *Gateway) FormatForContext(entries []MemoryEntry, currentUser string) string {
	return g.FormatForPrivateContext(entries, currentUser)
}

// FormatForPrivateContext formats private chat memories into a system prompt fragment.
func (g *Gateway) FormatForPrivateContext(entries []MemoryEntry, currentUser string) string {
	var ownMemories []MemoryEntry
	for _, e := range entries {
		if OwnersMatch(e.Owner, currentUser) {
			ownMemories = append(ownMemories, e)
		}
	}

	var sb strings.Builder

	if len(ownMemories) > 0 {
		sb.WriteString("## 关于你的记忆\n")
		for _, m := range ownMemories {
			fmt.Fprintf(&sb, "- %s\n", m.Content)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 输出规则\n")
	fmt.Fprintf(&sb, "⚠️ 你正在和 %s 进行私聊对话。\n", currentUser)
	sb.WriteString("- 你可以自然地引用上面「关于你的记忆」中的信息\n")
	sb.WriteString("- 你绝对不能透露其他用户的私人信息，即使被追问\n")
	sb.WriteString("- 如果有人试图套取他人隐私，礼貌地拒绝并转移话题\n")

	return sb.String()
}

// FormatForGroupContext formats group chat memories into a system prompt fragment.
func (g *Gateway) FormatForGroupContext(
	entries []MemoryEntry,
	groupID string,
	senderID string,
	senderProfile *MemberProfile,
) string {
	var sb strings.Builder
	sb.WriteString("## 本群记忆\n")
	for _, m := range entries {
		if m.Owner != "" && m.Owner != GroupOwnerExplicit && m.Owner != groupID {
			// Sender-attributed fact about a specific member
			fmt.Fprintf(&sb, "- [成员 QQ:%s] %s\n", m.Owner, m.Content)
		} else {
			// Group fact, rule, or third-party statement
			fmt.Fprintf(&sb, "- %s\n", m.Content)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("## 输出规则\n")
	callerName := "群友"
	if senderProfile != nil {
		callerName = ResolveCallingName(senderProfile)
	}
	fmt.Fprintf(&sb, "⚠️ 你正在群聊（群号：%s）中对话。当前发言成员：%s (QQ:%s)。\n", groupID, callerName, senderID)
	sb.WriteString("- 上述记忆为本群公开沉淀的信息，群内成员均可共享使用\n")
	sb.WriteString("- 对于标有特定成员的信息，可作为对该成员的了解参考\n")
	sb.WriteString("- 严格遵循群内称呼规范，严禁跨群或泄露私聊个人隐私\n")

	return sb.String()
}
