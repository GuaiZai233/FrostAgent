package llm

import (
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

const (
	// EnvGroupCompactWhitelistEnabled explicitly enables or disables group compact whitelist mode.
	EnvGroupCompactWhitelistEnabled = "ENABLE_GROUP_COMPACT_WHITELIST"
	// EnvGroupCompactWhitelist lists the groups allowed to run running compact when whitelist mode is on.
	EnvGroupCompactWhitelist = "GROUP_COMPACT_WHITELIST"

	// GroupCompactStatusPrompt is injected into a group turn's prompt when compaction is disabled for that group.
	GroupCompactStatusPrompt = "<group_compact_status>群聊 compact 已禁用：本群未在群聊压缩白名单中，本轮不提供群聊压缩总结，仅保留未压缩的最近群消息。</group_compact_status>"
)

// GroupCompactAllowed reports whether running compact may operate on the conversation.
// It reuses the proactive reply whitelist semantics: when the switch is unset, a non-empty list
// implies enabled; once enabled, only listed groups pass and an empty list rejects every group.
// Non-group conversations are not affected by the group compact whitelist.
func GroupCompactAllowed(getenv func(string) string, conversationID string) bool {
	if !proactive.IsWhitelistEnabledWith(getenv, EnvGroupCompactWhitelistEnabled, EnvGroupCompactWhitelist) {
		return true
	}
	identity, isGroup := groupIdentityFromConversationID(conversationID)
	if !isGroup {
		return true
	}
	if identity == "" {
		return false
	}
	return proactive.IsGroupAllowedWith(getenv, EnvGroupCompactWhitelistEnabled, EnvGroupCompactWhitelist, identity)
}

// WithoutCompactSummary removes the running summary and summary groups from a group context
// snapshot for a group outside the compact whitelist. Uncompacted recent messages are kept.
func WithoutCompactSummary(snap GroupContextSnapshot) GroupContextSnapshot {
	snap.RunningSummary = ""
	snap.SummaryGroups = nil
	snap.CompactDisabled = true
	return snap
}

// groupIdentityFromConversationID converts a session conversation ID into the identity used by
// whitelist entries. It mirrors the frontend groupIdFromSessionId: "group:<id>" and
// "<onebot|qq>:group:<id>" normalize to the bare QQ id, other platforms keep their prefix.
// The second result reports whether the conversation is a group conversation at all.
func groupIdentityFromConversationID(conversationID string) (string, bool) {
	id := strings.TrimSpace(conversationID)
	lower := strings.ToLower(id)
	if strings.HasPrefix(lower, "group:") {
		return strings.TrimSpace(id[len("group:"):]), true
	}
	const marker = ":group:"
	index := strings.LastIndex(lower, marker)
	if index < 0 {
		return "", false
	}
	prefix := strings.ToLower(strings.TrimSpace(id[:index]))
	raw := strings.TrimSpace(id[index+len(marker):])
	switch prefix {
	case "", "onebot", "qq":
		return raw, true
	default:
		if raw == "" {
			return "", true
		}
		return prefix + ":" + raw, true
	}
}
