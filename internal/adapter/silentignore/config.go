// Package silentignore parses the instance-level list of users whose inbound
// events are dropped before any processing, reply or persistence.
package silentignore

import (
	"strings"
	"unicode"
)

// EnvKey is the instance setting that holds the ignore list.
const EnvKey = "SILENT_IGNORE_USER_IDS"

// Config is an immutable snapshot of the ignore list.
type Config struct {
	users map[string]struct{}
}

// Parse reads the comma, semicolon or whitespace separated list. A bare ID
// matches every platform; "platform:ID" matches only that platform.
func Parse(getenv func(string) string) Config {
	cfg := Config{users: make(map[string]struct{})}
	if getenv == nil {
		return cfg
	}
	for _, item := range strings.FieldsFunc(getenv(EnvKey), func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	}) {
		if platform, id, ok := strings.Cut(item, ":"); ok {
			cfg.users[strings.ToLower(platform)+":"+id] = struct{}{}
		} else {
			cfg.users[item] = struct{}{}
		}
	}
	return cfg
}

// Ignored reports whether the user must be dropped silently.
func (c Config) Ignored(platform, userID string) bool {
	if userID == "" || len(c.users) == 0 {
		return false
	}
	if _, ok := c.users[userID]; ok {
		return true
	}
	_, ok := c.users[strings.ToLower(platform)+":"+userID]
	return ok
}
