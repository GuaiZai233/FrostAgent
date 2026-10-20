// Package poke normalizes inbound poke notifications and limits dialogue wakes.
package poke

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const DefaultCooldownSeconds = 30

func Cooldown(getenv func(string) string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(getenv("POKE_COOLDOWN_SECONDS")))
	if err != nil || seconds < 0 || seconds > 86400 {
		seconds = DefaultCooldownSeconds
	}
	return time.Duration(seconds) * time.Second
}

func Message(text string) string {
	text = strings.TrimSpace(text)
	if text != "" {
		text = " " + text
	}
	return "（用户戳了戳你" + text + "）"
}

var sequence atomic.Uint64

func ID() string {
	return fmt.Sprintf("poke_%d_%d", time.Now().UnixNano(), sequence.Add(1))
}

// Gate is shared by all connections of one runtime. Group keys exclude the
// actor so different users cannot bypass cooldown. Retention matches the
// maximum supported cooldown, allowing hot changes to use the last wake time.
type Gate struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// Ready checks an existing cooldown without reserving or extending it.
// Callers must still use Allow after eligibility checks for atomic admission.
func (g *Gate) Ready(key string, now time.Time, cooldown time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	last, ok := g.last[key]
	return !ok || now.Sub(last) >= cooldown
}

func (g *Gate) Allow(key string, now time.Time, cooldown time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if last, ok := g.last[key]; ok && now.Sub(last) < cooldown {
		return false
	}
	if g.last == nil {
		g.last = make(map[string]time.Time)
	}
	for key, last := range g.last {
		if now.Sub(last) >= 24*time.Hour {
			delete(g.last, key)
		}
	}
	g.last[key] = now
	return true
}
