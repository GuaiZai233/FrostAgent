package onebot

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/poke"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

const (
	defaultBannedRejectCooldownSeconds = 180
	maxBannedRejectCooldownSeconds     = 86400
)

// bannedRejectCooldown reads BANNED_REJECT_COOLDOWN_SECONDS, falling back to the
// default when the value is missing, malformed, or out of range.
func bannedRejectCooldown(getenv func(string) string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(getenv("BANNED_REJECT_COOLDOWN_SECONDS")))
	if err != nil || seconds < 0 || seconds > maxBannedRejectCooldownSeconds {
		seconds = defaultBannedRejectCooldownSeconds
	}
	return time.Duration(seconds) * time.Second
}

// isBannedRejection reports whether a blocking decision was caused by the
// principal being banned (locked) rather than by content or a review failure.
func isBannedRejection(controller *security.Controller, p security.Principal, decision security.WatchdogDecision) bool {
	return decision.Action == security.WatchdogLock ||
		decision.Reason == security.ErrLocked.Error() ||
		controller.IsLocked(p)
}

// allowBannedRejectReply decides whether a security rejection may be sent to a
// banned user. Pokes and bare name mentions in groups stay silent. A group @
// mention gets at most one reply per cooldown window per user and conversation;
// the slot is reserved when the reply is allowed. Private messages keep their
// existing behavior.
func allowBannedRejectReply(event model.OneBotEvent, wake GroupWakeSignals, gate *poke.Gate, getenv func(string) string, key string, now time.Time) bool {
	if event.PokeID != "" || wake.Poke {
		return false
	}
	if event.MessageType != "group" {
		return true
	}
	if !wake.AtBot {
		return false
	}
	return gate.Allow(fmt.Sprintf("banned-reject:%s", key), now, bannedRejectCooldown(getenv))
}
