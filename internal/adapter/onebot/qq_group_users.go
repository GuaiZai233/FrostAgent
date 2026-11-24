package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

const (
	qqGroupUserListMax        = 35
	qqGroupUserSearchDefault  = 10
	maxQQGroupMemberResponse  = 8 << 20
	qqGroupUserListTooLargeZH = "当前群人数过多，请使用group_user_search搜索特定群友的昵称。"
)

type qqGroupMember struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname,omitempty"`
	Card     string `json:"card,omitempty"`
	Role     string `json:"role,omitempty"`
}

// qqGroupUserAction reports whether the action is one of the member lookups.
func qqGroupUserAction(action string) bool {
	return action == "group_user_count" || action == "group_user_list" || action == "group_user_search"
}

// readQQGroupUsers serves the read-only member lookups. The group is always the
// active conversation: a model-chosen group would let it enumerate other groups.
func (c *wsConnection) readQQGroupUsers(ctx context.Context, event model.OneBotEvent, turn *llm.SessionTurn, request tools.QQRequest) (core.PlatformActionResult, error) {
	if turn.Interrupted() {
		return core.PlatformActionResult{}, context.Canceled
	}
	if event.MessageType != "group" || event.GroupID == 0 {
		return core.PlatformActionResult{}, fmt.Errorf("群成员查询仅可在群聊会话中使用")
	}
	call := func(action model.OneBotAction) (oneBotAPIResponse, error) {
		if turn.Interrupted() {
			return oneBotAPIResponse{}, context.Canceled
		}
		return c.sendActionAndWaitContext(action, actionACKTimeout(c.Scope), turn, ctx)
	}
	return runQQGroupUsers(event.GroupID, request, call)
}

func runQQGroupUsers(groupID int64, request tools.QQRequest, call func(model.OneBotAction) (oneBotAPIResponse, error)) (core.PlatformActionResult, error) {
	var payload any
	switch request.Action {
	case "group_user_count":
		count, err := qqGroupMemberCount(groupID, call)
		if err != nil {
			return core.PlatformActionResult{}, err
		}
		payload = map[string]any{"status": "ok", "group_user_count": count}
	case "group_user_list":
		members, err := loadQQGroupMembers(groupID, call)
		if err != nil {
			return core.PlatformActionResult{}, err
		}
		if len(members) > qqGroupUserListMax {
			return core.PlatformActionResult{}, fmt.Errorf("%s", qqGroupUserListTooLargeZH)
		}
		payload = map[string]any{"status": "ok", "group_user_count": len(members), "users": members}
	case "group_user_search":
		members, err := loadQQGroupMembers(groupID, call)
		if err != nil {
			return core.PlatformActionResult{}, err
		}
		limit := request.Arguments.Limit
		if limit == 0 {
			limit = qqGroupUserSearchDefault
		}
		matches := searchQQGroupMembers(members, request.Arguments.Query)
		total := len(matches)
		if total > limit {
			matches = matches[:limit]
		}
		payload = map[string]any{"status": "ok", "query": sanitizeContextName(request.Arguments.Query), "total_matches": total, "users": matches}
	default:
		return core.PlatformActionResult{}, fmt.Errorf("unknown QQ capability %q", request.Action)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return core.PlatformActionResult{}, err
	}
	return core.PlatformActionResult{Content: string(encoded)}, nil
}

// qqGroupMemberCount prefers the cheap group-info action and falls back to the
// member list when a gateway does not report member_count.
func qqGroupMemberCount(groupID int64, call func(model.OneBotAction) (oneBotAPIResponse, error)) (int, error) {
	response, err := call(model.OneBotAction{Action: "get_group_info", Params: map[string]any{"group_id": groupID, "no_cache": true}})
	if err == nil {
		var data struct {
			MemberCount int `json:"member_count"`
		}
		if json.Unmarshal(response.Data, &data) == nil && data.MemberCount > 0 {
			return data.MemberCount, nil
		}
	}
	members, listErr := loadQQGroupMembers(groupID, call)
	if listErr != nil {
		return 0, listErr
	}
	return len(members), nil
}

func loadQQGroupMembers(groupID int64, call func(model.OneBotAction) (oneBotAPIResponse, error)) ([]qqGroupMember, error) {
	response, err := call(model.OneBotAction{Action: "get_group_member_list", Params: map[string]any{"group_id": groupID, "no_cache": true}})
	if err != nil {
		return nil, err
	}
	if len(response.Data) > maxQQGroupMemberResponse {
		return nil, fmt.Errorf("群成员列表响应超过 8 MiB")
	}
	var raw []struct {
		UserID   json.Number `json:"user_id"`
		Nickname string      `json:"nickname"`
		Card     string      `json:"card"`
		Role     string      `json:"role"`
	}
	if err := json.Unmarshal(response.Data, &raw); err != nil {
		return nil, fmt.Errorf("群成员列表响应无效: %w", err)
	}
	members := make([]qqGroupMember, 0, len(raw))
	for _, item := range raw {
		id := item.UserID.String()
		if !positiveDecimal(id) {
			continue
		}
		member := qqGroupMember{UserID: id, Nickname: sanitizeContextName(item.Nickname), Card: sanitizeContextName(item.Card), Role: sanitizeContextName(item.Role)}
		if member.Card == member.Nickname {
			member.Card = ""
		}
		members = append(members, member)
	}
	return members, nil
}

func positiveDecimal(s string) bool {
	if s == "" || strings.Trim(s, "0") == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// searchQQGroupMembers ranks members by the best name match: exact, prefix,
// substring, then in-order subsequence (typo-tolerant for abbreviations).
func searchQQGroupMembers(members []qqGroupMember, query string) []qqGroupMember {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}
	type scored struct {
		member qqGroupMember
		rank   int
		length int
	}
	var hits []scored
	for _, member := range members {
		best, length := -1, 0
		for _, name := range []string{member.Card, member.Nickname} {
			if name == "" {
				continue
			}
			if rank := qqNameMatchRank(strings.ToLower(name), needle); rank >= 0 && (best < 0 || rank < best) {
				best, length = rank, len([]rune(name))
			}
		}
		if best >= 0 {
			hits = append(hits, scored{member, best, length})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		if hits[i].length != hits[j].length {
			return hits[i].length < hits[j].length
		}
		return hits[i].member.UserID < hits[j].member.UserID
	})
	out := make([]qqGroupMember, len(hits))
	for i, hit := range hits {
		out[i] = hit.member
	}
	return out
}

func qqNameMatchRank(name, needle string) int {
	switch {
	case name == needle:
		return 0
	case strings.HasPrefix(name, needle):
		return 1
	case strings.Contains(name, needle):
		return 2
	}
	remaining := []rune(needle)
	for _, r := range name {
		if len(remaining) > 0 && r == remaining[0] {
			remaining = remaining[1:]
		}
	}
	if len(remaining) == 0 {
		return 3
	}
	return -1
}
