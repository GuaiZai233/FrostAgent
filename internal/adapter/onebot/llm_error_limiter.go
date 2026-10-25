package onebot

import (
	"sync"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

// llmErrorReplyLimiter is shared by all connections of one adapter instance.
// Only admitted replies advance the rolling window, so drops do not extend it.
type llmErrorReplyLimiter struct {
	mu   sync.Mutex
	sent [3]time.Time
	next int
}

func (l *llmErrorReplyLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allowAt(time.Now())
}

// allowAt requires l.mu to be held when called concurrently.
func (l *llmErrorReplyLimiter) allowAt(now time.Time) bool {
	oldest := l.sent[l.next]
	if !oldest.IsZero() && now.Sub(oldest) < time.Minute {
		return false
	}
	l.sent[l.next] = now
	l.next = (l.next + 1) % len(l.sent)
	return true
}

func (c *wsConnection) allowLLMErrorReply(result llm.AgentRunResult) bool {
	// Direct Chat must neither consume nor be hidden by the production quota.
	if !result.LLMFailed || c.mock || c.llmErrorReplies == nil {
		return true
	}
	return c.llmErrorReplies.allow()
}
