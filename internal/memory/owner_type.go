package memory

import (
	"fmt"
	"strings"
)

// NormalizeOwnerType 把零值或未知值归一为合法 OwnerType。
// 老 brain.json 没有 owner_type 字段时 entry.OwnerType == ""，视为 user。
func NormalizeOwnerType(t OwnerType) OwnerType {
	switch t {
	case OwnerUser, OwnerGroup:
		return t
	default:
		return OwnerUser
	}
}

// IsQQPlatform reports whether a platform identifier refers to QQ.
// Native OneBot, AstrBot aiocqhttp adapter, and canonical "qq" all represent the QQ ecosystem.
func IsQQPlatform(platform string) bool {
	p := strings.ToLower(strings.TrimSpace(platform))
	return p == "" || p == "qq" || p == "onebot" || p == "aiocqhttp"
}

// CanonicalPlatform returns the normalized platform identifier.
// QQ family platforms (onebot, aiocqhttp, qq, "") map to "qq".
func CanonicalPlatform(platform string) string {
	if IsQQPlatform(platform) {
		return "qq"
	}
	return strings.ToLower(strings.TrimSpace(platform))
}

// OwnerForPrivate 生成私聊 owner 与对应 OwnerType。
// userID 形如 "123456"（OneBot 数字字符串），owner 与之一致。
func OwnerForPrivate(userID string) (string, OwnerType) {
	if userID == "" {
		return "", OwnerUser
	}
	return userID, OwnerUser
}

// OwnerForGroup 生成群聊 owner 与对应 OwnerType。
// 群 owner 形如 "group:123456"，与 userID 命名空间天然不冲突。
func OwnerForGroup(groupID int64) (string, OwnerType) {
	if groupID <= 0 {
		return "", OwnerGroup
	}
	return fmt.Sprintf("group:%d", groupID), OwnerGroup
}

// OwnerForPlatformPrivate 生成带平台前缀的私聊 owner。
// 当 platform 为空、"onebot"、"qq" 或 "aiocqhttp" 时保持与历史一致（即直接使用 userID 作为 owner）。
func OwnerForPlatformPrivate(platform, userID string) (string, OwnerType) {
	if userID == "" {
		return "", OwnerUser
	}
	if IsQQPlatform(platform) {
		return userID, OwnerUser
	}
	return fmt.Sprintf("%s:user:%s", strings.ToLower(strings.TrimSpace(platform)), userID), OwnerUser
}

// OwnerForPlatformGroup 生成带平台前缀的群聊 owner。
// 当 platform 为空、"onebot"、"qq" 或 "aiocqhttp" 时保持与历史一致（形如 "group:123456"）。
func OwnerForPlatformGroup(platform, groupID string) (string, OwnerType) {
	if groupID == "" {
		return "", OwnerGroup
	}
	if IsQQPlatform(platform) {
		return fmt.Sprintf("group:%s", groupID), OwnerGroup
	}
	return fmt.Sprintf("%s:group:%s", strings.ToLower(strings.TrimSpace(platform)), groupID), OwnerGroup
}

// CanonicalOwner normalizes legacy platform-prefixed QQ owners to the canonical format:
// - "aiocqhttp:user:<id>", "qq:user:<id>", "onebot:user:<id>" -> "<id>"
// - "aiocqhttp:group:<id>", "qq:group:<id>", "onebot:group:<id>" -> "group:<id>"
// Other platform owners are preserved.
func CanonicalOwner(owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return ""
	}
	for _, prefix := range []string{"aiocqhttp:user:", "qq:user:", "onebot:user:"} {
		if cut, ok := strings.CutPrefix(owner, prefix); ok {
			return cut
		}
	}
	for _, prefix := range []string{"aiocqhttp:group:", "qq:group:", "onebot:group:"} {
		if cut, ok := strings.CutPrefix(owner, prefix); ok {
			return "group:" + cut
		}
	}
	return owner
}

// OwnersMatch reports whether two owner identifiers refer to the same logical owner,
// accounting for legacy and canonical QQ owner prefixes.
func OwnersMatch(a, b string) bool {
	if a == b {
		return true
	}
	return CanonicalOwner(a) == CanonicalOwner(b)
}

// OwnerAliases returns all historical and canonical alias forms of an owner.
func OwnerAliases(owner string) []string {
	canonical := CanonicalOwner(owner)
	if canonical == "" {
		return nil
	}
	if groupID, ok := strings.CutPrefix(canonical, "group:"); ok {
		return []string{
			canonical,
			fmt.Sprintf("aiocqhttp:group:%s", groupID),
			fmt.Sprintf("qq:group:%s", groupID),
			fmt.Sprintf("onebot:group:%s", groupID),
		}
	}
	// Private user (non-colon)
	if !strings.Contains(canonical, ":") {
		return []string{
			canonical,
			fmt.Sprintf("aiocqhttp:user:%s", canonical),
			fmt.Sprintf("qq:user:%s", canonical),
			fmt.Sprintf("onebot:user:%s", canonical),
		}
	}
	return []string{canonical}
}

// CanonicalSessionKey converts legacy or platform-prefixed session keys for QQ
// into canonical keys ("group:<id>" or "private:<id>").
func CanonicalSessionKey(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	for _, prefix := range []string{"aiocqhttp:group:", "qq:group:", "onebot:group:"} {
		if cut, ok := strings.CutPrefix(sessionID, prefix); ok {
			return "group:" + cut
		}
	}
	for _, prefix := range []string{"aiocqhttp:private:", "qq:private:", "onebot:private:"} {
		if cut, ok := strings.CutPrefix(sessionID, prefix); ok {
			return "private:" + cut
		}
	}
	return sessionID
}

// SessionKeyAliases returns all alias representations of a session key.
func SessionKeyAliases(sessionID string) []string {
	canonical := CanonicalSessionKey(sessionID)
	if canonical == "" {
		return nil
	}
	if id, ok := strings.CutPrefix(canonical, "group:"); ok {
		return []string{
			canonical,
			fmt.Sprintf("aiocqhttp:group:%s", id),
			fmt.Sprintf("qq:group:%s", id),
			fmt.Sprintf("onebot:group:%s", id),
		}
	}
	if id, ok := strings.CutPrefix(canonical, "private:"); ok {
		return []string{
			canonical,
			fmt.Sprintf("aiocqhttp:private:%s", id),
			fmt.Sprintf("qq:private:%s", id),
			fmt.Sprintf("onebot:private:%s", id),
		}
	}
	return []string{canonical}
}

// CanonicalSessionID generates the canonical session identifier for an event.
func CanonicalSessionID(platform, messageType, id string) string {
	id = strings.TrimSpace(id)
	if IsQQPlatform(platform) {
		if messageType == "group" {
			return fmt.Sprintf("group:%s", id)
		}
		return fmt.Sprintf("private:%s", id)
	}
	p := strings.ToLower(strings.TrimSpace(platform))
	if p == "" {
		p = "astrbot"
	}
	if messageType == "group" {
		return fmt.Sprintf("%s:group:%s", p, id)
	}
	return fmt.Sprintf("%s:private:%s", p, id)
}
