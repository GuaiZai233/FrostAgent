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

	// PromptPrefix is prepended to the prompt when proactive reply is triggered.
	PromptPrefix = "此为触发主动回复逻辑的消息，如果你认为值得插嘴，请回复；反之，对于你不感兴趣的话题/领域、说了一半的话等，请调用stay_slient工具静默。"

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
