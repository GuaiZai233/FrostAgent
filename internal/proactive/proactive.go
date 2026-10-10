package proactive

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
)

const (
	// EnvProbability is the environment variable for proactive reply probability.
	EnvProbability = "PROACTIVE_REPLY_PROBABILITY"

	// EnvEnabled is an optional environment variable to explicitly enable or disable proactive reply.
	EnvEnabled = "ENABLE_PROACTIVE_REPLY"

	// EnvGroupWhitelist is the environment variable for proactive reply group whitelist (comma-separated).
	EnvGroupWhitelist = "PROACTIVE_REPLY_GROUP_WHITELIST"

	// EnvWhitelistEnabled is the environment variable to explicitly enable or disable group whitelist mode.
	EnvWhitelistEnabled = "ENABLE_PROACTIVE_REPLY_WHITELIST"

	// PromptPrefix is prepended to the prompt when proactive reply is triggered.
	PromptPrefix = "此为触发主动回复逻辑的消息，如果你认为值得插嘴，请回复；反之，对于你不感兴趣的话题/领域、说了一半的话等，请调用stay_silent工具静默。"

	// MinProbability defines the minimum probability when proactive reply is turned on (0.01).
	MinProbability = 0.01

	// MaxProbability defines the maximum probability for proactive reply (1.00).
	MaxProbability = 1.00
)

// ParseProbability parses and normalizes a probability string.
// If val is <= 0 or empty/invalid, it returns 0 (disabled).
// If val > 0, it is clamped to [MinProbability, MaxProbability] with 2 decimal precision.
func ParseProbability(val string) float64 {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}
	p, err := strconv.ParseFloat(val, 64)
	if err != nil || p <= 0 {
		return 0
	}
	if p < MinProbability {
		p = MinProbability
	} else if p > MaxProbability {
		p = MaxProbability
	}
	// Round to 2 decimal places
	return math.Round(p*100) / 100
}

// GetProbability extracts the effective proactive reply probability from environment getter.
// Returns 0 if disabled or unset. Once opened, returns at least 0.01.
func GetProbability(getenv func(string) string) float64 {
	if getenv == nil {
		return 0
	}
	if strings.EqualFold(strings.TrimSpace(getenv(EnvEnabled)), "false") {
		return 0
	}
	raw := strings.TrimSpace(getenv(EnvProbability))
	if raw == "" {
		if strings.EqualFold(strings.TrimSpace(getenv(EnvEnabled)), "true") {
			return MinProbability
		}
		return 0
	}
	return ParseProbability(raw)
}

// Roll evaluates whether an inbound message hits the proactive reply probability.
func Roll(getenv func(string) string) bool {
	return RollWithRand(getenv, rand.Float64)
}

// RollWithRand evaluates whether an inbound message hits using a custom RNG function.
func RollWithRand(getenv func(string) string, rng func() float64) bool {
	prob := GetProbability(getenv)
	if prob <= 0 {
		return false
	}
	if rng == nil {
		rng = rand.Float64
	}
	roll := rng()
	return roll < prob
}

// ParseGroupWhitelist parses a comma-, newline-, semicolon-, or whitespace-delimited group ID string into a set.
func ParseGroupWhitelist(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	items := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	result := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}

// IsWhitelistEnabled reports whether group whitelist mode is enabled for proactive reply.
// Returns true if ENABLE_PROACTIVE_REPLY_WHITELIST is "true".
// Returns false if ENABLE_PROACTIVE_REPLY_WHITELIST is "false".
// If unset, returns true if PROACTIVE_REPLY_GROUP_WHITELIST contains at least one non-empty entry.
func IsWhitelistEnabled(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	en := strings.TrimSpace(getenv(EnvWhitelistEnabled))
	if strings.EqualFold(en, "true") {
		return true
	}
	if strings.EqualFold(en, "false") {
		return false
	}
	whitelist := ParseGroupWhitelist(getenv(EnvGroupWhitelist))
	return len(whitelist) > 0
}

// NormalizePlatform canonicalizes messaging platform identifiers.
// OneBot, aiocqhttp, and qq aliases are normalized to "qq".
func NormalizePlatform(platform string) string {
	p := strings.ToLower(strings.TrimSpace(platform))
	switch p {
	case "onebot", "aiocqhttp", "qq":
		return "qq"
	default:
		return p
	}
}

// IsGroupAllowed checks if the given group ID (and optional platform) is permitted to trigger proactive reply.
// When whitelist mode is disabled, all groups are allowed (returns true).
// Once whitelist mode is enabled:
// - Non-QQ platform groups match platform-qualified entries (e.g. "telegram:123") or bare legacy entries ("123").
// - QQ/OneBot groups match bare entries ("123") or "qq:123" / "onebot:123" / "aiocqhttp:123".
// - Empty whitelist under enabled mode rejects all groups.
func IsGroupAllowed(getenv func(string) string, groupID string, platforms ...string) bool {
	if !IsWhitelistEnabled(getenv) {
		return true
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return false
	}
	whitelist := ParseGroupWhitelist(getenv(EnvGroupWhitelist))
	if len(whitelist) == 0 {
		return false
	}

	platform := ""
	if len(platforms) > 0 {
		platform = platforms[0]
	}

	targetPlatform := NormalizePlatform(platform)
	targetGroupID := groupID
	if p, gid, ok := strings.Cut(groupID, ":"); ok && p != "" {
		if targetPlatform == "" || targetPlatform == "qq" {
			targetPlatform = NormalizePlatform(p)
		}
		targetGroupID = strings.TrimSpace(gid)
	}

	// 1. Direct match with raw groupID
	if _, ok := whitelist[groupID]; ok {
		return true
	}

	// 2. Bare groupID match (e.g. "123456")
	// If the whitelist has bare "123456", it matches QQ groups and bare legacy entries
	if _, ok := whitelist[targetGroupID]; ok {
		return true
	}

	// 3. Platform-qualified match
	if targetPlatform != "" {
		if targetPlatform == "qq" {
			for _, alias := range []string{"qq", "onebot", "aiocqhttp"} {
				if _, ok := whitelist[alias+":"+targetGroupID]; ok {
					return true
				}
			}
		} else {
			if _, ok := whitelist[targetPlatform+":"+targetGroupID]; ok {
				return true
			}
		}
	}

	return false
}

// RollGroup evaluates whether an inbound group message hits the proactive reply probability and satisfies whitelist rules.
func RollGroup(getenv func(string) string, groupID string, platforms ...string) bool {
	return RollGroupWithRand(getenv, groupID, rand.Float64, platforms...)
}

// RollGroupWithRand evaluates whether an inbound group message hits using a custom RNG function and group whitelist check.
// Once whitelist mode is active, non-whitelist groups never trigger proactive reply.
func RollGroupWithRand(getenv func(string) string, groupID string, rng func() float64, platforms ...string) bool {
	if !IsGroupAllowed(getenv, groupID, platforms...) {
		return false
	}
	return RollWithRand(getenv, rng)
}
