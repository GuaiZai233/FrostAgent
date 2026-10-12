package botloop

import (
	"strconv"
	"strings"
	"unicode"
)

const PromptPrefix = "此用户被 FrostAgent 管理员标记为 Bot，为避免递归，强烈建议你结束话题并调用stay_silent工具忽略后续请求。"

type Config struct {
	users    map[string]struct{}
	limit    int
	hard     bool
	revision string
}

func Parse(getenv func(string) string) Config {
	cfg := Config{users: make(map[string]struct{}), limit: 3}
	if getenv == nil {
		return cfg
	}
	users := getenv("BOT_LOOP_USER_IDS")
	maxTurns := getenv("BOT_LOOP_MAX_TURNS")
	mode := getenv("BOT_LOOP_MODE")
	cfg.revision = users + "\x00" + maxTurns + "\x00" + mode
	for _, user := range strings.FieldsFunc(users, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	}) {
		if platform, userID, ok := strings.Cut(user, ":"); ok {
			cfg.users[strings.ToLower(platform)+":"+userID] = struct{}{}
		} else {
			cfg.users[user] = struct{}{}
		}
	}
	if limit, err := strconv.Atoi(strings.TrimSpace(maxTurns)); err == nil && limit > 0 {
		cfg.limit = limit
	}
	cfg.hard = strings.EqualFold(strings.TrimSpace(mode), "hard")
	return cfg
}

func (c Config) IsMarked(platform, userID string) bool {
	if userID == "" {
		return false
	}
	if _, ok := c.users[userID]; ok {
		return true
	}
	_, ok := c.users[strings.ToLower(platform)+":"+userID]
	return ok
}

func (c Config) Limit() int       { return c.limit }
func (c Config) Hard() bool       { return c.hard }
func (c Config) Revision() string { return c.revision }
