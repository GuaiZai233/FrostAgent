package memory

import (
	"strings"
	"time"
)

// GroupRole represents the role of a QQ group member.
type GroupRole string

const (
	GroupRoleUnknown GroupRole = "unknown"
	GroupRoleMember  GroupRole = "member"
	GroupRoleAdmin   GroupRole = "admin"
	GroupRoleOwner   GroupRole = "owner"
)

// NormalizeGroupRole converts an arbitrary string to a valid GroupRole.
func NormalizeGroupRole(r string) GroupRole {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "owner":
		return GroupRoleOwner
	case "admin", "administrator":
		return GroupRoleAdmin
	case "member":
		return GroupRoleMember
	default:
		return GroupRoleUnknown
	}
}

// MemberProfile stores the structured persistent profile of an observed group member.
type MemberProfile struct {
	UserID        string    `json:"user_id"`                  // QQ 号（唯一键）
	Nickname      string    `json:"nickname"`                 // QQ 昵称
	Card          string    `json:"card,omitempty"`           // 群名片（仅用于身份消歧，不作称呼）
	Role          GroupRole `json:"role"`                     // 群角色: owner / admin / member / unknown
	PreferredName string    `json:"preferred_name,omitempty"` // 主要通识名/偏好称呼
	Aliases       []string  `json:"aliases,omitempty"`        // 别名列表
	Source        string    `json:"source,omitempty"`         // 来源
	CreatedAt     time.Time `json:"created_at"`               // 首次记录时间
	UpdatedAt     time.Time `json:"updated_at"`               // 档案更新时间
	LastSpokeAt   time.Time `json:"last_spoke_at"`            // 最近发言时间
}

// GroupProfile persistently stores group name and observed speaking members.
type GroupProfile struct {
	GroupID   string                    `json:"group_id"`   // 群号
	GroupName string                    `json:"group_name"` // 群名称
	Members   map[string]*MemberProfile `json:"members"`    // 发言成员档案 (key: user_id)
	UpdatedAt time.Time                 `json:"updated_at"` // 更新时间
}

// GetMember returns the member profile for userID if present.
func (p *GroupProfile) GetMember(userID string) *MemberProfile {
	if p == nil || p.Members == nil {
		return nil
	}
	return p.Members[userID]
}

// ResolveCallingName resolves how the bot should address the member.
func (m *MemberProfile) ResolveCallingName() string {
	return ResolveCallingName(m)
}

// ResolveCallingName resolves how the bot should address the member.
// Priority:
// 1. Primary preferred name (preferred_name)
// 2. QQ nickname (nickname)
// 3. Fallback neutral address ("群友")
//
// Invariant: Group card (card) is STRICTLY for disambiguation and identity verification;
// it must NEVER be returned as the calling name, nor prioritized over nickname.
func ResolveCallingName(m *MemberProfile) string {
	if m == nil {
		return "群友"
	}
	if p := strings.TrimSpace(m.PreferredName); p != "" {
		return p
	}
	if n := strings.TrimSpace(m.Nickname); n != "" {
		return n
	}
	return "群友"
}

// MemberContextPrompt generates a guidance string for the LLM when interacting with this member.
func MemberContextPrompt(m *MemberProfile) string {
	if m == nil {
		return ""
	}
	callingName := ResolveCallingName(m)
	var sb strings.Builder
	sb.WriteString("成员 [QQ:" + m.UserID + "] 推荐称呼：" + callingName)
	if m.Card != "" && m.Card != m.Nickname && m.Card != m.PreferredName {
		sb.WriteString("（群名片：" + m.Card + "，仅作识别，禁止直接作为称呼）")
	}
	if len(m.Aliases) > 0 {
		sb.WriteString("，已知别名：" + strings.Join(m.Aliases, "、"))
	}
	switch m.Role {
	case GroupRoleOwner:
		sb.WriteString("，群身份：群主")
	case GroupRoleAdmin:
		sb.WriteString("，群身份：管理员")
	}
	return sb.String()
}
