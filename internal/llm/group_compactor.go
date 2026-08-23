package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/logs"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	groupCompactPrompt = `你是群聊上下文压缩器。请将已有总结与新群消息合并成一份简洁、可继续滚动更新的总结。删除闲聊和重复表达。
不要增加对话中没有的信息。只输出总结正文，不要 Markdown 标题或解释。

{conversation}`

	DefaultGroupCompactMaxBufferSize = 200
	DefaultGroupCompactMinInterval   = 30 * time.Second
	// DefaultGroupCompactRetryDelay 是异步压缩失败后的最小重试退避时间（Minimum Retry Backoff）。
	// 实际重试延迟会同时遵守 minInterval 冷却，最终延迟为 max(retryDelay, remaining cooldown)。
	DefaultGroupCompactRetryDelay = 5 * time.Second
)

type pendingPersistRecord struct {
	owner           string
	summary         string
	storeGeneration uint64
	retryCount      int
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

	mu                sync.Mutex
	inflight          map[string]bool
	lastRun           map[string]time.Time
	scheduled         map[string]*time.Timer
	scheduledToken    map[string]uint64
	scheduledTimerSeq map[string]uint64

	pendingPersist map[string]*pendingPersistRecord
	persistActive  map[string]bool
	persistWake    map[string]chan struct{}
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
	maxBufferSize := max(bufferSize*10, DefaultGroupCompactMaxBufferSize)
	return &GroupCompactor{
		provider:          provider,
		store:             store,
		model:             model,
		bufferSize:        bufferSize,
		maxBufferSize:     maxBufferSize,
		minInterval:       minInterval,
		retryDelay:        DefaultGroupCompactRetryDelay,
		inflight:          make(map[string]bool),
		lastRun:           make(map[string]time.Time),
		scheduled:         make(map[string]*time.Timer),
		scheduledToken:    make(map[string]uint64),
		scheduledTimerSeq: make(map[string]uint64),
		pendingPersist:    make(map[string]*pendingPersistRecord),
		persistActive:     make(map[string]bool),
		persistWake:       make(map[string]chan struct{}),
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

// SetMaxBufferSize updates the maximum in-memory uncompacted buffer ceiling.
// Invariant: maxBufferSize must be >= bufferSize.
func (c *GroupCompactor) SetMaxBufferSize(size int) {
	if c == nil || size <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if size < c.bufferSize {
		logs.Warn(
			logs.SYSTEM,
			fmt.Sprintf(
				"GroupCompactor: maxBufferSize (%d) < bufferSize (%d)，已自动修正为 bufferSize (%d) 以维持 invariants",
				size,
				c.bufferSize,
				c.bufferSize,
			),
		)
		size = c.bufferSize
	}
	c.maxBufferSize = size
}

// SetBufferSize updates the batch trigger size and enforces maxBufferSize >= bufferSize.
func (c *GroupCompactor) SetBufferSize(size int) {
	if c == nil || size <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bufferSize = size
	if c.maxBufferSize < c.bufferSize {
		logs.Warn(
			logs.SYSTEM,
			fmt.Sprintf(
				"GroupCompactor: maxBufferSize (%d) < bufferSize (%d)，已自动扩展为 bufferSize (%d)",
				c.maxBufferSize,
				c.bufferSize,
				c.bufferSize,
			),
		)
		c.maxBufferSize = c.bufferSize
	}
}

func (c *GroupCompactor) SetRetryDelay(delay time.Duration) {
	if c == nil || delay <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retryDelay = delay
}

// HasPendingPersistence reports whether an owner currently has an unpersisted dirty summary.
func (c *GroupCompactor) HasPendingPersistence(owner string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pendingPersist[owner] != nil
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

	if !c.lastRun[key].IsZero() {
		elapsed := time.Since(c.lastRun[key])
		// Allow a 5ms grace window for timer precision jitter so a timer that woke up
		// slightly before the nominal boundary does not bounce into another timer cycle.
		if elapsed+5*time.Millisecond < c.minInterval {
			if session.GroupCompactReady(c.bufferSize) && c.scheduled[key] == nil {
				delay := c.minInterval - elapsed + 5*time.Millisecond
				c.scheduleTriggerLocked(session, owner, delay)
			}
			c.mu.Unlock()
			return
		}
	}

	snapshot, ready := session.SnapshotGroupCompact(c.bufferSize)
	if !ready {
		c.mu.Unlock()
		return
	}

	c.cancelScheduledLocked(key)

	c.inflight[key] = true
	c.lastRun[key] = time.Now()
	c.mu.Unlock()

	storeGeneration := uint64(0)
	if c.store != nil {
		storeGeneration = c.store.Generation(owner)
	}
	go c.compact(session, owner, snapshot, storeGeneration)
}

func (c *GroupCompactor) scheduleTriggerLocked(session *SessionContext, owner string, delay time.Duration) {
	key := session.ConversationID
	if c.scheduled[key] != nil {
		return
	}
	if delay < 0 {
		delay = 0
	}
	c.scheduledTimerSeq[key]++
	token := c.scheduledTimerSeq[key]
	c.scheduledToken[key] = token

	c.scheduled[key] = time.AfterFunc(delay, func() {
		c.mu.Lock()
		if c.scheduledToken[key] != token {
			// Stale callback from an older timer: do not delete the newer timer entry.
			c.mu.Unlock()
			return
		}
		delete(c.scheduled, key)
		delete(c.scheduledToken, key)
		c.mu.Unlock()
		c.Trigger(session, owner)
	})
}

func (c *GroupCompactor) cancelScheduledLocked(key string) {
	if timer := c.scheduled[key]; timer != nil {
		timer.Stop()
		delete(c.scheduled, key)
		delete(c.scheduledToken, key)
	}
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
				// Retry delay respects minInterval cooldown: max(retryDelay, remaining cooldown)
				delay = c.retryDelay
				if cd := c.minInterval - time.Since(lastRun); cd > delay {
					delay = cd
				}
			}
			if delay > 0 {
				delay += 5 * time.Millisecond
			}
			c.scheduleTriggerLocked(session, owner, delay)
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
	if reqBytes, mErr := json.Marshal(request); mErr == nil {
		logs.Info(logs.LLM_REQUEST, string(reqBytes))
	}
	response, err := c.provider.Chat(context.Background(), request)
	if err != nil {
		logs.Error(logs.LLM_RESPONSE, fmt.Sprintf("群聊 running compact LLM调用失败 (%s): %v", owner, err))
		logs.Warn(logs.SYSTEM, fmt.Sprintf("群聊 running compact 失败 (%s): %v", owner, err))
		return
	}
	if respBytes, mErr := json.Marshal(response); mErr == nil {
		logs.Info(logs.LLM_RESPONSE, string(respBytes))
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
	logs.Info(logs.SYSTEM, fmt.Sprintf("群聊 running compact 已更新 (%s, %d 条新消息)", owner, len(snapshot.Messages)))
	succeeded = true

	c.queuePersistence(owner, summary, storeGeneration)
}

func (c *GroupCompactor) queuePersistence(owner, summary string, storeGeneration uint64) {
	if c.store == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.pendingPersist[owner] = &pendingPersistRecord{
		owner:           owner,
		summary:         summary,
		storeGeneration: storeGeneration,
		retryCount:      0,
	}

	if c.persistActive[owner] {
		// Worker is already running for this owner.
		// Notify the worker so that if it is currently sleeping in a backoff timer,
		// it wakes up immediately to process this latest pending summary.
		wakeCh := c.persistWake[owner]
		select {
		case wakeCh <- struct{}{}:
		default:
		}
		return
	}

	// No active worker for this owner; launch one.
	c.persistActive[owner] = true
	wakeCh := make(chan struct{}, 1)
	c.persistWake[owner] = wakeCh
	go c.persistWorker(owner, wakeCh)
}

func (c *GroupCompactor) persistWorker(owner string, wakeCh chan struct{}) {
	for {
		c.mu.Lock()
		target := c.pendingPersist[owner]
		if target == nil {
			c.persistActive[owner] = false
			delete(c.persistWake, owner)
			c.mu.Unlock()
			return
		}
		rec := *target
		c.mu.Unlock()

		applied, err := c.store.Upsert(rec.owner, rec.summary, rec.storeGeneration)
		if err != nil {
			logs.Warn(
				logs.SYSTEM,
				fmt.Sprintf("群聊总结持久化失败，将后台独立重试 (%s, retry %d): %v", owner, rec.retryCount, err),
			)

			c.mu.Lock()
			cur := c.pendingPersist[owner]
			if cur == nil {
				c.persistActive[owner] = false
				delete(c.persistWake, owner)
				c.mu.Unlock()
				return
			}
			if cur.summary == rec.summary && cur.storeGeneration == rec.storeGeneration {
				cur.retryCount++
			}
			delay := calculatePersistRetryDelay(cur.retryCount)
			c.mu.Unlock()

			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-wakeCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			continue
		}

		if !applied {
			logs.Info(logs.SYSTEM, fmt.Sprintf("已丢弃删除后的群聊总结持久化 (%s)", owner))
		}

		c.mu.Lock()
		if cur := c.pendingPersist[owner]; cur != nil {
			// If the record in pendingPersist hasn't been updated with a newer summary while Upsert was running,
			// it has been successfully persisted.
			if cur.summary == rec.summary && cur.storeGeneration == rec.storeGeneration {
				delete(c.pendingPersist, owner)
			}
			// If cur was replaced by a newer summary while Upsert was executing,
			// pendingPersist remains populated and the next loop iteration will persist it!
		}
		c.mu.Unlock()
	}
}

func calculatePersistRetryDelay(retryCount int) time.Duration {
	switch retryCount {
	case 1:
		return 50 * time.Millisecond
	case 2:
		return 150 * time.Millisecond
	case 3:
		return 500 * time.Millisecond
	case 4:
		return 1 * time.Second
	default:
		return 2 * time.Second
	}
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
