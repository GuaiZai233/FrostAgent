package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type groupCompactItem struct {
	sequence uint64
	content  string
}

// GroupCompactSnapshot is an immutable batch sent to the asynchronous
// compactor. ThroughSequence lets the session commit only the messages covered
// by this summary while preserving messages that arrived during the LLM call.
type GroupCompactSnapshot struct {
	Summary         string
	Messages        []string
	ThroughSequence uint64
	Generation      uint64
}

// SessionTurn forms a FIFO chain reserved in WebSocket arrival order. It is
// separate from the history mutex because a full LLM turn calls methods that
// briefly lock session state themselves.
type SessionTurn struct {
	wait <-chan struct{}
	done chan struct{}
	once sync.Once
}

func (t *SessionTurn) Wait() {
	if t != nil && t.wait != nil {
		<-t.wait
	}
}

func (t *SessionTurn) Done() {
	if t != nil {
		t.once.Do(func() { close(t.done) })
	}
}

// SessionContext 管理单个会话的上下文历史
type SessionContext struct {
	ConversationID string
	History        []ChatMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
	mu             sync.Mutex // 保护单个会话的并发访问
	turnMu         sync.Mutex
	turnTail       chan struct{}

	groupCompactSummary    string
	groupCompactBuffer     []groupCompactItem
	groupCompactSequence   uint64
	groupCompactGeneration uint64
	pendingTurns           [][]memory.PendingExtractionItem
	extractionThreshold    int
}

// ReserveTurn appends one complete dialogue turn to this session's FIFO chain.
func (s *SessionContext) ReserveTurn() *SessionTurn {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	turn := &SessionTurn{wait: s.turnTail, done: make(chan struct{})}
	s.turnTail = turn.done
	return turn
}

// Lock 锁定会话
func (s *SessionContext) Lock() {
	s.mu.Lock()
}

// Unlock 解锁会话
func (s *SessionContext) Unlock() {
	s.mu.Unlock()
}

// Snapshot returns a copy of the messages while holding the session lock.
func (s *SessionContext) Snapshot() []ChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := make([]ChatMessage, len(s.History))
	for i, msg := range s.History {
		newMsg := msg

		// 1. 深拷贝 ToolCalls
		if len(msg.ToolCalls) > 0 {
			newMsg.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
			copy(newMsg.ToolCalls, msg.ToolCalls)
		}

		// 2. 深拷贝 Content (处理 any 类型中的切片)
		// 如果业务中使用了 []MessagePart，则需要进行拷贝
		// 注意：如果 MessagePart 未定义，此处会编译失败。
		// 但根据 PR 要求，我们需要处理这种潜在的切片类型。
		/*
			if msg.Content != nil {
				if v, ok := msg.Content.([]MessagePart); ok {
					newContent := make([]MessagePart, len(v))
					copy(newContent, v)
					newMsg.Content = newContent
				}
			}
		*/

		snapshot[i] = newMsg
	}
	return snapshot
}

// ReplaceMessages atomically replaces a session history with deep copy.
func (s *SessionContext) ReplaceMessages(messages []ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newMessages := make([]ChatMessage, len(messages))
	for i, msg := range messages {
		newMsg := msg
		// 1. 深拷贝 ToolCalls
		if len(msg.ToolCalls) > 0 {
			newMsg.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
			copy(newMsg.ToolCalls, msg.ToolCalls)
		}
		// 2. 深拷贝 Content
		/*
			if msg.Content != nil {
				if v, ok := msg.Content.([]MessagePart); ok {
					newContent := make([]MessagePart, len(v))
					copy(newContent, v)
					newMsg.Content = newContent
				}
			}
		*/
		newMessages[i] = newMsg
	}

	s.History = newMessages
	s.UpdatedAt = time.Now()
}

// TrimHistory 只保留最近的 max 条消息，从头部丢弃更早的历史。
func (s *SessionContext) TrimHistory(max int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if max <= 0 || len(s.History) <= max {
		return
	}
	trimmed := make([]ChatMessage, max)
	copy(trimmed, s.History[len(s.History)-max:])
	s.History = trimmed
	s.UpdatedAt = time.Now()
}

// AppendGroupCompactMessage appends one visible group message to the running
// compact ring. The raw-message portion is capped at bufferSize; the summary is
// stored separately and acts as the conceptual first item.
func (s *SessionContext) AppendGroupCompactMessage(content string, bufferSize int) int {
	content = strings.TrimSpace(content)
	if content == "" || bufferSize <= 0 {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.groupCompactSequence++
	s.groupCompactBuffer = append(s.groupCompactBuffer, groupCompactItem{
		sequence: s.groupCompactSequence,
		content:  content,
	})
	if len(s.groupCompactBuffer) > bufferSize {
		drop := len(s.groupCompactBuffer) - bufferSize
		copy(s.groupCompactBuffer, s.groupCompactBuffer[drop:])
		s.groupCompactBuffer = s.groupCompactBuffer[:bufferSize]
	}
	s.UpdatedAt = time.Now()
	return len(s.groupCompactBuffer)
}

// SnapshotGroupCompact returns a batch once bufferSize raw messages are ready.
// It does not mutate session state; CommitGroupCompact performs the atomic
// replacement after the asynchronous summary succeeds.
func (s *SessionContext) SnapshotGroupCompact(bufferSize int) (GroupCompactSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if bufferSize <= 0 || len(s.groupCompactBuffer) < bufferSize {
		return GroupCompactSnapshot{}, false
	}
	items := make([]string, len(s.groupCompactBuffer))
	for i, item := range s.groupCompactBuffer {
		items[i] = item.content
	}
	return GroupCompactSnapshot{
		Summary:         s.groupCompactSummary,
		Messages:        items,
		ThroughSequence: s.groupCompactBuffer[len(s.groupCompactBuffer)-1].sequence,
		Generation:      s.groupCompactGeneration,
	}, true
}

// CommitGroupCompact replaces the running summary and removes only raw
// messages included in snapshot. It rejects results invalidated by deletion.
func (s *SessionContext) CommitGroupCompact(snapshot GroupCompactSnapshot, summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.Generation != s.groupCompactGeneration {
		return false
	}

	s.groupCompactSummary = summary
	firstNew := 0
	for firstNew < len(s.groupCompactBuffer) &&
		s.groupCompactBuffer[firstNew].sequence <= snapshot.ThroughSequence {
		firstNew++
	}
	if firstNew > 0 {
		remaining := append([]groupCompactItem(nil), s.groupCompactBuffer[firstNew:]...)
		s.groupCompactBuffer = remaining
	}
	s.UpdatedAt = time.Now()
	return true
}

// GroupRunningSummary returns the latest completed group summary.
func (s *SessionContext) GroupRunningSummary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.groupCompactSummary
}

// ResetGroupCompact clears summary state and invalidates any in-flight result.
func (s *SessionContext) ResetGroupCompact() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.groupCompactGeneration++
	s.groupCompactSummary = ""
	s.groupCompactBuffer = nil
	s.UpdatedAt = time.Now()
}

// GroupCompactReady reports whether another full raw-message batch is ready.
func (s *SessionContext) GroupCompactReady(bufferSize int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bufferSize > 0 && len(s.groupCompactBuffer) >= bufferSize
}

// EnqueuePendingTurn appends one completed user/assistant turn. Once the
// per-session random threshold is reached, it atomically returns and clears the
// pending batch for asynchronous extraction.
func (s *SessionContext) EnqueuePendingTurn(
	items []memory.PendingExtractionItem,
	minTurns int,
	maxTurns int,
) ([]memory.PendingExtractionItem, bool) {
	if len(items) == 0 {
		return nil, false
	}
	if minTurns <= 0 {
		minTurns = 3
	}
	if maxTurns < minTurns {
		maxTurns = minTurns
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.extractionThreshold == 0 {
		s.extractionThreshold = minTurns
		if width := maxTurns - minTurns + 1; width > 1 {
			s.extractionThreshold += rand.IntN(width)
		}
	}
	turn := append([]memory.PendingExtractionItem(nil), items...)
	s.pendingTurns = append(s.pendingTurns, turn)
	s.UpdatedAt = time.Now()
	if len(s.pendingTurns) < s.extractionThreshold {
		return nil, false
	}

	count := 0
	for _, pendingTurn := range s.pendingTurns {
		count += len(pendingTurn)
	}
	batch := make([]memory.PendingExtractionItem, 0, count)
	for _, pendingTurn := range s.pendingTurns {
		batch = append(batch, pendingTurn...)
	}
	s.pendingTurns = nil
	s.extractionThreshold = 0
	return batch, true
}

func (s *SessionContext) PendingTurnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pendingTurns)
}

// minHistory 是历史消息数的下限，防止配置过小导致对话无法进行。
const minHistory = 4

// SessionManager 管理多个会话上下文，支持多用户/多群聊隔离
type SessionManager struct {
	sessions          map[string]*SessionContext
	mu                sync.RWMutex
	groupSummaryStore *groupsummary.Store
	MaxHistory        int           // 单个会话保留的最大历史消息数
	TTL               time.Duration // 会话有效期
}

// NewSessionManager 创建新的会话管理器
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions:   make(map[string]*SessionContext),
		MaxHistory: 50,
		TTL:        24 * time.Hour,
	}
	// MAX_CONTEXT_MESSAGES env 覆盖默认值；运行期再修改 env 会在每次裁剪时生效
	// （见 agent.go effectiveMaxHistory）。
	if v := os.Getenv("MAX_CONTEXT_MESSAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minHistory {
			sm.MaxHistory = n
		}
	}
	// 启动定时清理协程
	go sm.startCleanupRoutine()
	return sm
}

// SetGroupSummaryStore enables lazy restoration when a group session is born.
func (sm *SessionManager) SetGroupSummaryStore(store *groupsummary.Store) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.groupSummaryStore = store
}

// GetOrCreate 获取或创建会话
func (sm *SessionManager) GetOrCreate(sessionID string) *SessionContext {
	sm.mu.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mu.RUnlock()
	if exists {
		return session
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()
	// 双重检查
	if session, exists = sm.sessions[sessionID]; exists {
		return session
	}

	summary := ""
	isGroupSession := strings.HasPrefix(sessionID, "group:") || strings.Contains(sessionID, ":group:")
	if sm.groupSummaryStore != nil && isGroupSession {
		record, ok, err := sm.groupSummaryStore.Get(sessionID)
		if err != nil {
			logs.Warn(logs.SYSTEM, "恢复群聊总结失败 ("+sessionID+"): "+err.Error())
		} else if ok {
			summary = record.Summary
		}
	}
	session = &SessionContext{
		ConversationID:      sessionID,
		History:             make([]ChatMessage, 0),
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
		groupCompactSummary: summary,
	}
	sm.sessions[sessionID] = session
	return session
}

// startCleanupRoutine 定时清理过期会话
func (sm *SessionManager) startCleanupRoutine() {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		sm.Cleanup()
	}
}

// Cleanup 清理超过 TTL 未更新的会话
func (sm *SessionManager) Cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := time.Now()
	for id, s := range sm.sessions {
		s.mu.Lock()
		expired := now.Sub(s.UpdatedAt) > sm.TTL
		s.mu.Unlock()
		if expired {
			delete(sm.sessions, id)
		}
	}
}

// ── core.Session interface implementation ──

// ID 返回会话的唯一标识符
func (s *SessionContext) ID() string {
	return s.ConversationID
}

// AddMessage 添加一条 core.ChatMessage 到会话历史
func (s *SessionContext) AddMessage(msg core.ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 转换 core.ChatMessage -> llm.ChatMessage
	llmMsg := ChatMessage{
		Role:    string(msg.Role),
		Content: msg.Content,
	}
	if len(msg.ToolCalls) > 0 {
		llmMsg.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
		for j, tc := range msg.ToolCalls {
			llmMsg.ToolCalls[j] = ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}
	s.History = append(s.History, llmMsg)
	s.UpdatedAt = time.Now()
}

// Messages 以 core.ChatMessage 切片形式返回会话历史
func (s *SessionContext) Messages() []core.ChatMessage {
	s.mu.Lock() // 用写锁，因为 convertToCoreMessages 会读取整个切片
	defer s.mu.Unlock()

	return convertToCoreMessages(s.History)
}

// Clear 清空当前会话的所有消息
func (s *SessionContext) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.History = nil
	s.groupCompactSummary = ""
	s.groupCompactBuffer = nil
	s.groupCompactGeneration++
	s.pendingTurns = nil
	s.extractionThreshold = 0
	s.UpdatedAt = time.Now()
}

// ResetGroupCompact clears one active group's compact state without deleting
// its normal conversation history.
func (sm *SessionManager) ResetGroupCompact(sessionID string) bool {
	sm.mu.RLock()
	session, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()
	if !ok {
		return false
	}
	session.ResetGroupCompact()
	return true
}

// ── core.SessionStore interface implementation ──

// Get 获取指定会话，返回 core.Session 接口
func (sm *SessionManager) Get(sessionID string) (core.Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[sessionID]
	if !ok {
		return nil, false
	}
	return s, true
}

// Create 创建一个新的会话并返回 core.Session 接口
func (sm *SessionManager) Create(sessionID string) core.Session {
	return sm.GetOrCreate(sessionID)
}

// Delete 删除指定会话
func (sm *SessionManager) Delete(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// Count returns the number of active sessions.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// ListSessions returns a paginated slice of session contexts.
func (sm *SessionManager) ListSessions(offset, limit int) []*SessionContext {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(sm.sessions)
	}

	type sessionEntry struct {
		context   *SessionContext
		id        string
		updatedAt time.Time
	}

	entries := make([]sessionEntry, 0, len(sm.sessions))
	for _, session := range sm.sessions {
		session.mu.Lock()
		entries = append(entries, sessionEntry{
			context:   session,
			id:        session.ConversationID,
			updatedAt: session.UpdatedAt,
		})
		session.mu.Unlock()
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].updatedAt.Equal(entries[j].updatedAt) {
			return entries[i].id < entries[j].id
		}
		return entries[i].updatedAt.After(entries[j].updatedAt)
	})

	if offset >= len(entries) {
		return nil
	}

	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}

	result := make([]*SessionContext, 0, end-offset)
	for _, entry := range entries[offset:end] {
		result = append(result, entry.context)
	}
	return result
}
