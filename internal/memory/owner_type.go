package memory

import "fmt"

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
