package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const groupCompactPrompt = `你是群聊上下文压缩器。请将已有总结与新群消息合并成一份简洁、可继续滚动更新的总结。
保留话题、重要事实、决定、参与者立场和未解决问题；删除闲聊和重复表达。
不要增加对话中没有的信息。只输出总结正文，不要 Markdown 标题或解释。

{conversation}`

// GroupCompactor asynchronously turns a bounded group-message ring into a
// running summary. Only one request per group may be in flight.
type GroupCompactor struct {
	provider    core.LLMProvider
	writer      *memory.Writer
	model       string
	bufferSize  int
	minInterval time.Duration

	mu       sync.Mutex
	inflight map[string]bool
	lastRun  map[string]time.Time
}

func NewGroupCompactor(
	provider core.LLMProvider,
	writer *memory.Writer,
	model string,
	bufferSize int,
	minInterval time.Duration,
) *GroupCompactor {
	if bufferSize <= 0 {
		bufferSize = 20
	}
	if minInterval <= 0 {
		minInterval = 30 * time.Second
	}
	return &GroupCompactor{
		provider:    provider,
		writer:      writer,
		model:       model,
		bufferSize:  bufferSize,
		minInterval: minInterval,
		inflight:    make(map[string]bool),
		lastRun:     make(map[string]time.Time),
	}
}

func (c *GroupCompactor) BufferSize() int {
	if c == nil {
		return 0
	}
	return c.bufferSize
}

// Trigger starts compaction when a full batch is ready. Failures are logged and
// never block OneBot event processing.
func (c *GroupCompactor) Trigger(session *SessionContext, owner string) {
	if c == nil || session == nil || c.provider == nil || c.model == "" || owner == "" {
		return
	}
	key := session.ConversationID

	c.mu.Lock()
	if c.inflight[key] || time.Since(c.lastRun[key]) < c.minInterval {
		c.mu.Unlock()
		return
	}
	snapshot, ready := session.SnapshotGroupCompact(c.bufferSize)
	if !ready {
		c.mu.Unlock()
		return
	}
	c.inflight[key] = true
	c.lastRun[key] = time.Now()
	c.mu.Unlock()

	go c.compact(session, owner, snapshot)
}

func (c *GroupCompactor) compact(
	session *SessionContext,
	owner string,
	snapshot GroupCompactSnapshot,
) {
	succeeded := false
	defer func() {
		c.mu.Lock()
		delete(c.inflight, session.ConversationID)
		lastRun := c.lastRun[session.ConversationID]
		c.mu.Unlock()

		if succeeded && session.GroupCompactReady(c.bufferSize) {
			delay := c.minInterval - time.Since(lastRun)
			if delay < 0 {
				delay = 0
			}
			time.AfterFunc(delay, func() { c.Trigger(session, owner) })
		}
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

	session.CommitGroupCompact(snapshot, summary)
	if c.writer != nil {
		if err := c.writer.WriteCompact(owner, summary); err != nil {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("持久化群聊 running compact 失败 (%s): %v", owner, err))
		}
	}
	logs.Info(logs.SYSTEM, fmt.Sprintf("群聊 running compact 已更新 (%s, %d 条新消息)", owner, len(snapshot.Messages)))
	succeeded = true
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
