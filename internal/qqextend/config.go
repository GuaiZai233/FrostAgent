package qqextend

import (
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"strconv"
	"strings"
	"unicode"
)

const ToolName = "qq_extend_messages"
const TargetGroupsKey = "QQ_EXTEND_MESSAGES_TARGET_GROUPS"

// ConfiguredTargets deliberately excludes process and injected environment
// fallback: an empty instance setting must revoke all cross-session targets.
func ConfiguredTargets(scope *runtimescope.Scope) string {
	if scope == nil || scope.Config == nil {
		return ""
	}
	return scope.Config.Get(TargetGroupsKey)
}

func ParseGroupID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, fmt.Errorf("目标群号必须为正整数")
	}
	return id, nil
}

// ParseTargetGroups rejects the entire configuration when any entry is invalid.
func ParseTargetGroups(raw string) ([]string, error) {
	groups := []string{}
	seen := map[string]bool{}
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
		if _, err := ParseGroupID(item); err != nil {
			return nil, err
		}
		if !seen[item] {
			groups = append(groups, item)
			seen[item] = true
		}
	}
	return groups, nil
}

func TargetAllowed(raw, groupID string) bool {
	groups, err := ParseTargetGroups(raw)
	if err != nil {
		return false
	}
	for _, group := range groups {
		if group == groupID {
			return true
		}
	}
	return false
}
