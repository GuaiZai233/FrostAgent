package proactive

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// EnvGroupProbabilities stores per-group overrides as a JSON object. Bare group
// IDs belong to QQ; other platforms use platform:group_id, like the whitelist.
const EnvGroupProbabilities = "PROACTIVE_REPLY_GROUP_PROBABILITIES"

var decimalProbability = regexp.MustCompile(`^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$`)

// ParseGroupProbability preserves zero (disable this group) and valid positive
// precision. Negative or invalid values use 0.01; values above one use one.
func ParseGroupProbability(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if !decimalProbability.MatchString(raw) {
		return MinProbability
	}
	p, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(p) || math.IsInf(p, 0) || p < 0 {
		return MinProbability
	}
	return math.Min(p, MaxProbability)
}

func groupProbabilityKey(groupID string, platforms ...string) string {
	platform := ""
	if len(platforms) > 0 {
		platform = strings.TrimSpace(platforms[0])
	}
	groupID = strings.TrimSpace(groupID)
	// An explicitly supplied platform owns the identity; colons inside its raw
	// group ID must never let it select another platform's override.
	if platform == "" {
		if p, id, ok := strings.Cut(groupID, ":"); ok {
			platform, groupID = p, strings.TrimSpace(id)
		}
	}
	platform = NormalizePlatform(platform)
	if platform == "" {
		platform = "qq"
	}
	return platform + ":" + groupID
}

// ParseGroupProbabilities validates the container and canonicalizes identities.
// Invalid individual probability values get the same fallback as the UI.
func ParseGroupProbabilities(raw string) (map[string]float64, error) {
	result := make(map[string]float64)
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &entries); err != nil || entries == nil {
		return nil, fmt.Errorf("群聊主动回复概率必须为 JSON 对象")
	}
	for id, value := range entries {
		id = strings.TrimSpace(id)
		if id == "" || strings.ContainsAny(id, ",;\x00") || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("群聊主动回复概率包含无效群标识")
		}
		if platform, group, ok := strings.Cut(id, ":"); ok && (platform == "" || group == "") {
			return nil, fmt.Errorf("群聊主动回复概率包含无效群标识")
		}
		key := groupProbabilityKey(id)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("群聊主动回复概率包含重复群标识 %q", key)
		}
		text := string(value)
		if len(value) > 0 && value[0] == '"' {
			_ = json.Unmarshal(value, &text)
		}
		result[key] = ParseGroupProbability(text)
	}
	return result, nil
}

// GetGroupProbability selects an override without bypassing the existing
// instance-level enable/disable semantics. Whitelist checks remain separate.
func GetGroupProbability(getenv func(string) string, groupID string, platforms ...string) float64 {
	probability := GetProbability(getenv)
	if !(probability > 0) {
		return 0
	}
	overrides, err := ParseGroupProbabilities(getenv(EnvGroupProbabilities))
	if err == nil {
		if p, ok := overrides[groupProbabilityKey(groupID, platforms...)]; ok {
			return p
		}
	}
	return probability
}
