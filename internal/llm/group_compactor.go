package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/logs"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	groupCompactPrompt = `你是群聊上下文压缩器。请将已有总结与新群消息合并成一份简洁、可继续滚动更新的总结。删除闲聊和重复表达。
不要增加对话中没有的信息。只输出总结正文，不要 Markdown 标题或解释。

{conversation}`

	DefaultGroupCompactBufferSize    = 20
	DefaultGroupCompactMaxBufferSize = 200
	DefaultGroupCompactMinInterval   = 30 * time.Second
	DefaultGroupCompactRetryDelay    = 5 * time.Second
)

var groupSummaryRetryDelays = [...]time.Duration{
	0,
	100 * time.Millisecond,
	300 * time.Millisecond,
	900 * time.Millisecond,
}

// GroupCompactor asynchronously turns a bounded group-message ring into a
// running summary. Only one request per group may be in flight.
type GroupCompactor struct {
	provider      core.LLMProvider
	store         *groupsummary.Store
	model         string
	bufferSize    int
	maxBufferSize int
	minInterval   time.Duration
	retryDelay    time.Duration

	mu        sync.Mutex
	inflight  map[string]bool
	lastRun   map[string]time.Time
	scheduled map[string]*time.Timer
}

// NewGroupCompactor creates a durable running summary compactor.
func NewGroupCompactor(
	provider core.LLMProvider,
	store *groupsummary.Store,
	model string,
	bufferSize int,
	minInterval time.Duration,
) *GroupCompactor {
	if bufferSize <= 0 {
		bufferSize = DefaultGroupCompactBufferSize
	}
	if minInterval <= 0 {
		minInterval = DefaultGroupCompactMinInterval
	}
	maxBufferSize := bufferSize * 10
	if maxBufferSize < DefaultGroupCompactMaxBufferSize {
		maxBufferSize = DefaultGroupCompactMaxBufferSize
	}
	return &GroupCompactor{
		provider:      provider,
		store:         store,
		model:         model,
		bufferSize:    bufferSize,
		maxBufferSize: maxBufferSize,
		minInterval:   minInterval,
		retryDelay:    DefaultGroupCompactRetryDelay,
		inflight:      make(map[string]bool),
		lastRun:       make(map[string]time.Time),
		scheduled:     make(map[string]*time.Timer),
	}
}

func (c *GroupCompactor) BufferSize() int {
	if c == nil {
		return 0
	}
	return c.bufferSize
}

func (c *GroupCompactor) MaxBufferSize() int {
	if c == nil {
		return 0
	}
	return c.maxBufferSize
}

func (c *GroupCompactor) SetMaxBufferSize(size int) {
	if c == nil || size <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxBufferSize = size
}

func (c *GroupCompactor) SetRetryDelay(delay time.Duration) {
	if c == nil || delay <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retryDelay = delay
}

// Trigger starts compaction when a full batch is ready. Failures are logged and
// never block OneBot event processing.
func (c *GroupCompactor) Trigger(session *SessionContext, owner string) {
	if c == nil || session == nil || c.provider == nil || c.model == "" || owner == "" {
		return
	}
	key := session.ConversationID

	c.mu.Lock()
	if c.inflight[key] {
		c.mu.Unlock()
		return
	}

	elapsed := time.Since(c.lastRun[key])
	if elapsed < c.minInterval {
		// Still in cooldown. If buffer is ready, schedule a delayed trigger when cooldown expires.
		if session.GroupCompactReady(c.bufferSize) && c.scheduled[key] == nil {
			delay := c.minInterval - elapsed
			c.scheduled[key] = time.AfterFunc(delay, func() {
				c.mu.Lock()
				delete(c.scheduled, key)
				c.mu.Unlock()
				c.Trigger(session, owner)
			})
		}
		c.mu.Unlock()
		return
	}

	snapshot, ready := session.SnapshotGroupCompact(c.bufferSize)
	if !ready {
		c.mu.Unlock()
		return
	}

	if timer := c.scheduled[key]; timer != nil {
		timer.Stop()
		delete(c.scheduled, key)
	}

	c.inflight[key] = true
	c.lastRun[key] = time.Now()
	c.mu.Unlock()

	storeGeneration := uint64(0)
	if c.store != nil {
		storeGeneration = c.store.Generation(owner)
	}
	go c.compact(session, owner, snapshot, storeGeneration)
}

func (c *GroupCompactor) compact(
	session *SessionContext,
	owner string,
	snapshot GroupCompactSnapshot,
	storeGeneration uint64,
) {
	succeeded := false
	defer func() {
		c.mu.Lock()
		key := session.ConversationID
		delete(c.inflight, key)
		lastRun := c.lastRun[key]

		if session.GroupCompactReady(c.bufferSize) && c.scheduled[key] == nil {
			var delay time.Duration
			if succeeded {
				delay = c.minInterval - time.Since(lastRun)
			} else {
				delay = c.retryDelay
				if cd := c.minInterval - time.Since(lastRun); cd > delay {
					delay = cd
				}
			}
			if delay < 0 {
				delay = 0
			}
			c.scheduled[key] = time.AfterFunc(delay, func() {
				c.mu.Lock()
				delete(c.scheduled, key)
				c.mu.Unlock()
				c.Trigger(session, owner)
			})
		}
		c.mu.Unlock()
	}()

	conversation := formatGroupCompactInput(snapshot)
	request := core.ChatRequest{
		Model: c.model,
		Messages: []core.ChatMessage{{
			Role:    core.RoleUser,
			Content: strings.Replace(groupCompactPrompt, "{conversation}", conversation, 1),
		}},
		MaxTokens:   1024,
		Temperature: 0.2,
	}
	response, err := c.provider.Chat(context.Background(), request)
	if err != nil {
		logs.Warn(logs.SYSTEM, fmt.Sprintf("群聊 running compact 失败 (%s): %v", owner, err))
		return
	}
	summary, ok := response.Message.Content.(string)
	summary = strings.TrimSpace(summary)
	if !ok || summary == "" {
		logs.Warn(logs.SYSTEM, fmt.Sprintf("群聊 running compact 返回空总结 (%s)", owner))
		return
	}

	if !session.CommitGroupCompact(snapshot, summary) {
		logs.Info(logs.SYSTEM, fmt.Sprintf("已丢弃被删除操作失效的群聊 running compact (%s)", owner))
		return
	}
	c.persistSummary(owner, summary, storeGeneration)
	logs.Info(logs.SYSTEM, fmt.Sprintf("群聊 running compact 已更新 (%s, %d 条新消息)", owner, len(snapshot.Messages)))
	succeeded = true
}

// persistSummary retries transient write failures without rolling back the
// in-memory summary. A later compact can persist the combined newer state.
func (c *GroupCompactor) persistSummary(
	owner string,
	summary string,
	storeGeneration uint64,
) {
	if c.store == nil {
		return
	}

	var lastErr error
	for _, delay := range groupSummaryRetryDelays {
		if delay > 0 {
			time.Sleep(delay)
		}
		applied, err := c.store.Upsert(owner, summary, storeGeneration)
		if err == nil {
			if !applied {
				logs.Info(logs.SYSTEM, fmt.Sprintf("已丢弃删除后的群聊总结持久化 (%s)", owner))
			}
			return
		}
		lastErr = err
	}
	logs.Error(logs.SYSTEM, fmt.Sprintf("群聊总结持久化失败，已重试 3 次 (%s): %v", owner, lastErr))
}

func formatGroupCompactInput(snapshot GroupCompactSnapshot) string {
	var builder strings.Builder
	if snapshot.Summary != "" {
		fmt.Fprintf(&builder, "[已有群聊总结]\n%s\n\n", snapshot.Summary)
	}
	for _, message := range snapshot.Messages {
		fmt.Fprintf(&builder, "[群消息] %s\n", message)
	}
	return builder.String()
}
