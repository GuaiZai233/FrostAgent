package sticker

import "strconv"

const (
	ActivityEnvKey      = "STICKER_ACTIVITY"
	ActivityModeEnvKey  = "STICKER_ACTIVITY_MODE"
	ActivityBotTurns    = "bot_turns"
	ActivityAllMessages = "all_messages"
)

// ActivityMode defaults legacy and invalid configuration to bot dialogue turns.
func ActivityMode(raw string) string {
	if raw == ActivityAllMessages {
		return ActivityAllMessages
	}
	return ActivityBotTurns
}

// ActivityLevel treats missing or invalid configuration as disabled.
func ActivityLevel(raw string) int {
	level, err := strconv.Atoi(raw)
	if err != nil || level < 0 || level > 10 {
		return 0
	}
	return level
}

func ActivityInterval(level int) int {
	if level < 1 || level > 10 {
		return 0
	}
	return (11 - level) * 5
}
