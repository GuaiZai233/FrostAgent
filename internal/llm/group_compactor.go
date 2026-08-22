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
	// DefaultGroupCompactRetryDelay 是异步压缩失败后的最小重试退避时间（Minimum Retry Backoff）。
	// 实际重试延迟会同时遵守 minInterval 冷却，最终延迟为 max(retryDelay, remaining cooldown)。
	DefaultGroupCompactRetryDelay = 5 * time.Second
)

type pendingPersistRecord struct {
	owner           string
	summary         string
	storeGeneration uint64
	sequence        uint64
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

	pendingPersist    map[string]*pendingPersistRecord
	persistSeq        map[string]uint64
	persistTimer      map[string]*time.Timer
	persistTimerToken map[string]uint64
	persistTimerSeq   map[string]uint64
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
		persistSeq:        make(map[string]uint64),
		persistTimer:      make(map[string]*time.Timer),
		persistTimerToken: make(map[string]uint64),
		persistTimerSeq:   make(map[string]uint64),
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
	logs.Info(logs.SYSTEM, fmt.Sprintf("群聊 running compact 已更新 (%s, %d 条新消息)", owner, len(snapshot.Messages)))
	succeeded = true

	c.queuePersistence(owner, summary, storeGeneration)
}

func (c *GroupCompactor) queuePersistence(owner, summary string, storeGeneration uint64) {
	if c.store == nil {
		return
	}

	c.mu.Lock()
	c.persistSeq[owner]++
	seq := c.persistSeq[owner]
	rec := &pendingPersistRecord{
		owner:           owner,
		summary:         summary,
		storeGeneration: storeGeneration,
		sequence:        seq,
		retryCount:      0,
	}
	c.pendingPersist[owner] = rec
	c.mu.Unlock()

	c.tryPersist(owner, rec)
}

func (c *GroupCompactor) tryPersist(owner string, rec *pendingPersistRecord) {
	if c.store == nil || rec == nil {
		return
	}

	c.mu.Lock()
	if rec.sequence < c.persistSeq[owner] {
		// A newer summary has already been produced or queued. Discard stale retry.
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	applied, err := c.store.Upsert(rec.owner, rec.summary, rec.storeGeneration)
	if err == nil {
		c.mu.Lock()
		if current := c.pendingPersist[owner]; current != nil && current.sequence <= rec.sequence {
			delete(c.pendingPersist, owner)
			if timer := c.persistTimer[owner]; timer != nil {
				timer.Stop()
				delete(c.persistTimer, owner)
				delete(c.persistTimerToken, owner)
			}
		}
		c.mu.Unlock()

		if !applied {
			logs.Info(logs.SYSTEM, fmt.Sprintf("已丢弃删除后的群聊总结持久化 (%s)", owner))
		}
		return
	}

	logs.Warn(
		logs.SYSTEM,
		fmt.Sprintf("群聊总结持久化失败，将后台独立重试 (%s, seq %d, retry %d): %v", owner, rec.sequence, rec.retryCount, err),
	)

	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.pendingPersist[owner]
	if current == nil || current.sequence != rec.sequence {
		return
	}

	current.retryCount++
	delay := calculatePersistRetryDelay(current.retryCount)
	c.schedulePersistRetryLocked(owner, current.sequence, delay)
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

func (c *GroupCompactor) schedulePersistRetryLocked(owner string, seq uint64, delay time.Duration) {
	if c.persistTimer[owner] != nil {
		return
	}
	c.persistTimerSeq[owner]++
	token := c.persistTimerSeq[owner]
	c.persistTimerToken[owner] = token

	c.persistTimer[owner] = time.AfterFunc(delay, func() {
		c.mu.Lock()
		if c.persistTimerToken[owner] != token {
			c.mu.Unlock()
			return
		}
		delete(c.persistTimer, owner)
		delete(c.persistTimerToken, owner)

		current := c.pendingPersist[owner]
		if current == nil || current.sequence != seq {
			c.mu.Unlock()
			return
		}
		rec := *current
		c.mu.Unlock()

		c.tryPersist(owner, &rec)
	})
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
