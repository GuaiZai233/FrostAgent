package llm

import (
	"os"
	"strconv"
	"strings"
)

const (
	// DefaultGroupCompactBufferSize is the fallback buffer size for group compaction and raw context.
	DefaultGroupCompactBufferSize = 20
	// DefaultGroupRawContextMaxChars is the default char budget for uncompacted group messages.
	DefaultGroupRawContextMaxChars = 12000
)

// GroupRawContextConfig holds the runtime configuration for uncompacted group raw context.
type GroupRawContextConfig struct {
	MaxChars int
}

// DefaultGroupRawContextConfig returns default raw group context settings.
func DefaultGroupRawContextConfig() GroupRawContextConfig {
	return GroupRawContextConfig{
		MaxChars: DefaultGroupRawContextMaxChars,
	}
}

// LoadGroupRawContextConfigFromEnv loads GroupRawContextConfig dynamically from runtime environment variables.
func LoadGroupRawContextConfigFromEnv() GroupRawContextConfig {
	cfg := DefaultGroupRawContextConfig()
	if v := strings.TrimSpace(os.Getenv("GROUP_RAW_CONTEXT_MAX_CHARS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxChars = n
		}
	}
	return cfg
}
