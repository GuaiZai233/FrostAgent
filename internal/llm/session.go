package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GroupCompactMessage represents a structured group chat message for compaction and prompt inspection.
type GroupCompactMessage struct {
	Role      string `json:"role"`                 // "user" | "assistant"
	Sender    string `json:"sender,omitempty"`     // Display name or nick
	SenderID  string `json:"sender_id,omitempty"`  // User ID or bot ID
	Content   string `json:"content"`              // Message body (opaque payload)
	MessageID string `json:"message_id,omitempty"` // Message ID for deduplication
	Time      string `json:"time,omitempty"`       // Timestamp string e.g. "15:04:05"
	raw       string `json:"-"`                    // Preserved raw string if ingested from text
}

// FormatDisplay renders the message as a single-line display string.
func (m GroupCompactMessage) FormatDisplay() string {
	if m.raw != "" {
		return m.raw
	}
	if m.Role == "assistant" {
		botName := m.Sender
		if botName == "" {
			botName = "霜降"
		}
		return fmt.Sprintf("[assistant] %s: %s", botName, m.Content)
	}
	sender := m.Sender
	if sender == "" {
		sender = "user"
	}
	if m.SenderID != "" {
		return fmt.Sprintf("[user] %s (%s): %s", sender, m.SenderID, m.Content)
	}
	return fmt.Sprintf("[user] %s: %s", sender, m.Content)
}

// ParseGroupCompactMessage parses a text line into a GroupCompactMessage while preserving the raw text.
func ParseGroupCompactMessage(content string, messageID ...string) GroupCompactMessage {
	content = strings.TrimSpace(content)
	if content == "" {
		return GroupCompactMessage{}
	}

	var msgID string
	if len(messageID) > 0 {
		msgID = strings.TrimSpace(messageID[0])
	}
	if msgID == "" {
		if start := strings.Index(content, "[msg_"); start != -1 {
			if end := strings.Index(content[start:], "]"); end != -1 {
				msgID = content[start+1 : start+end]
			}
		}
	}

	role := "user"
	text := content

	if strings.HasPrefix(strings.ToLower(text), "[assistant]") {
		role = "assistant"
		text = strings.TrimSpace(text[len("[assistant]"):])
	} else if strings.HasPrefix(strings.ToLower(text), "[user]") {
		role = "user"
		text = strings.TrimSpace(text[len("[user]"):])
	}

	colonIdx := strings.Index(text, ":")
	if colonIdx != -1 {
		senderPart := strings.TrimSpace(text[:colonIdx])
		msgBody := strings.TrimSpace(text[colonIdx+1:])

		sender := senderPart
		var senderID string

		if openParen := strings.Index(senderPart, "("); openParen != -1 {
			if closeParen := strings.Index(senderPart[openParen:], ")"); closeParen != -1 {
				sender = strings.TrimSpace(senderPart[:openParen])
				senderID = strings.TrimSpace(senderPart[openParen+1 : openParen+closeParen])
			}
		}

		return GroupCompactMessage{
			Role:      role,
			Sender:    sender,
			SenderID:  senderID,
			Content:   msgBody,
			MessageID: msgID,
			raw:       content,
		}
	}

	return GroupCompactMessage{
		Role:      role,
		Content:   content,
		MessageID: msgID,
		raw:       content,
	}
}

type groupCompactItem struct {
	sequence uint64
	message  GroupCompactMessage
}

// SummaryGroup maps the latest compact batch to the cumulative running summary
// produced after processing that batch. SessionContext retains only this latest
// bounded batch mapping; it is not a complete archive of all summarized source messages.
type SummaryGroup struct {
	Summary        string                `json:"summary"`
	MessageIDs     []string              `json:"message_ids,omitempty"`
	StartMessageID string                `json:"start_message_id,omitempty"`
	EndMessageID   string                `json:"end_message_id,omitempty"`
	StartIndex     int                   `json:"start_index,omitempty"`
	EndIndex       int                   `json:"end_index,omitempty"`
	Messages       []string              `json:"messages,omitempty"`
	StructuredMsgs []GroupCompactMessage `json:"structured_messages,omitempty"`
}

// GroupContextSnapshot captures an atomic point-in-time view of both the
// running summary and uncompacted recent messages from the same session state.
type GroupContextSnapshot struct {
	RunningSummary           string                `json:"running_summary"`
	RecentMessages           []string              `json:"recent_messages"`
	RecentStructuredMessages []GroupCompactMessage `json:"recent_structured_messages,omitempty"`
	SummaryGroups            []SummaryGroup        `json:"summary_groups,omitempty"`
}

// GroupCompactSnapshot is an immutable batch sent to the asynchronous
// compactor. ThroughSequence lets the session commit only the messages covered
// by this summary while preserving messages that arrived during the LLM call.
type GroupCompactSnapshot struct {
	Summary         string
	Messages        []GroupCompactMessage
	MessageIDs      []string
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

// DeliveryFailure records a transient failure when an assistant response could not be delivered to the platform.
type DeliveryFailure struct {
	Platform string `json:"platform"`
	Action   string `json:"action"`
	RetCode  int    `json:"retcode"`
	Message  string `json:"message"`
	Wording  string `json:"wording"`
}

// FormatDeliveryContext renders the DeliveryFailure as an XML tag for prompt injection.
func (f *DeliveryFailure) FormatDeliveryContext() string {
	if f == nil {
		return ""
	}
	reason := strings.TrimSpace(f.Wording)
	if reason == "" {
		reason = strings.TrimSpace(f.Message)
	}
	if reason == "" && f.RetCode != 0 {
		reason = fmt.Sprintf("retcode %d", f.RetCode)
	}
	if reason == "" {
		reason = "unknown delivery error"
	}

	platform := strings.TrimSpace(f.Platform)
	if platform == "" {
		platform = "onebot"
	}

	return fmt.Sprintf("<delivery_context>\nYour previous assistant response was not delivered to the user/group.\nPlatform: %s\nReason: %s\nDo not assume the user saw or received that response.\n</delivery_context>", platform, reason)
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
	groupSummaryGroups     []SummaryGroup
	pendingTurns           [][]memory.PendingExtractionItem
	extractionThreshold    int
	deliveryFailure        *DeliveryFailure

	// lastSystemPrompt records the dynamically-assembled system prompt from the
	// most recent real LLM call (time label + dialogue + memory catalog + recalled
	// memories). Empty until the first LLM request for this session.
	lastSystemPrompt string
	lastModelName    string
}

// SetDeliveryFailure records a transient delivery failure on the session.
func (s *SessionContext) SetDeliveryFailure(failure DeliveryFailure) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveryFailure = &failure
}

// TakeDeliveryFailure atomically retrieves and clears the pending delivery failure.
func (s *SessionContext) TakeDeliveryFailure() *DeliveryFailure {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deliveryFailure == nil {
		return nil
	}
	f := s.deliveryFailure
	s.deliveryFailure = nil
	return f
}

// SetLastPromptTrace records the system prompt and model used in the most recent LLM call.
func (s *SessionContext) SetLastPromptTrace(systemPrompt, modelName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSystemPrompt = systemPrompt
	s.lastModelName = modelName
}

// LastPromptTrace returns the system prompt and model from the most recent LLM call.
// Returns empty strings if no request has been made yet.
func (s *SessionContext) LastPromptTrace() (systemPrompt, modelName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSystemPrompt, s.lastModelName
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

// DefaultMaxGroupCompactBufferSize 是未压缩消息缓冲区的兜底安全上限，
// 防止在上游 LLM 长时间不可用且海量消息涌入时发生内存泄漏。
const DefaultMaxGroupCompactBufferSize = 200

// AppendGroupCompactMessage appends one visible group message to the running
// compact buffer. It accepts either a GroupCompactMessage, *GroupCompactMessage, or a string.
// The uncommitted buffer is capped at maxBufferSize (defaulting to DefaultMaxGroupCompactBufferSize if <= 0)
// to allow in-flight compactions to complete without eagerly dropping raw messages.
// Optional messageID can be provided to support deduplication with the triggering message.
func (s *SessionContext) AppendGroupCompactMessage(item any, maxBufferSize int, messageID ...string) int {
	var msg GroupCompactMessage
	switch v := item.(type) {
	case GroupCompactMessage:
		msg = v
		if len(messageID) > 0 && msg.MessageID == "" {
			msg.MessageID = strings.TrimSpace(messageID[0])
		}
	case *GroupCompactMessage:
		if v != nil {
			msg = *v
			if len(messageID) > 0 && msg.MessageID == "" {
				msg.MessageID = strings.TrimSpace(messageID[0])
			}
		}
	case string:
		msg = ParseGroupCompactMessage(v, messageID...)
	case fmt.Stringer:
		msg = ParseGroupCompactMessage(v.String(), messageID...)
	default:
		return 0
	}

	if strings.TrimSpace(msg.Content) == "" {
		return 0
	}
	if msg.Role == "" {
		msg.Role = "user"
	}
	if maxBufferSize <= 0 {
		maxBufferSize = DefaultMaxGroupCompactBufferSize
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.groupCompactSequence++
	s.groupCompactBuffer = append(s.groupCompactBuffer, groupCompactItem{
		sequence: s.groupCompactSequence,
		message:  msg,
	})
	if len(s.groupCompactBuffer) > maxBufferSize {
		drop := len(s.groupCompactBuffer) - maxBufferSize
		copy(s.groupCompactBuffer, s.groupCompactBuffer[drop:])
		s.groupCompactBuffer = s.groupCompactBuffer[:maxBufferSize]
	}
	s.UpdatedAt = time.Now()
	return len(s.groupCompactBuffer)
}

// AppendGroupCompactString parses a raw message string and appends it to the compact buffer.
func (s *SessionContext) AppendGroupCompactString(content string, maxBufferSize int, messageID ...string) int {
	return s.AppendGroupCompactMessage(content, maxBufferSize, messageID...)
}

// SnapshotGroupContext atomically retrieves the running summary and uncompacted recent messages
// from the same session state while holding the session lock.
func (s *SessionContext) SnapshotGroupContext(limit int, maxChars int, excludeMessageID string) GroupContextSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	recentMessages := s.recentPendingGroupMessagesLocked(limit, maxChars, excludeMessageID)

	return GroupContextSnapshot{
		RunningSummary:           s.groupCompactSummary,
		RecentMessages:           recentMessages,
		RecentStructuredMessages: s.recentPendingStructuredGroupMessagesLocked(limit, maxChars, excludeMessageID),
		SummaryGroups:            s.groupSummaryGroups,
	}
}

// recentPendingStructuredGroupMessagesLocked returns the same bounded recent
// message window as recentPendingGroupMessagesLocked while preserving trusted
// role/sender metadata. Callers can serialize these records without flattening
// untrusted multi-line content back into apparent chat-role boundaries.
func (s *SessionContext) recentPendingStructuredGroupMessagesLocked(limit int, maxChars int, excludeMessageID string) []GroupCompactMessage {
	if limit <= 0 || maxChars <= 0 || len(s.groupCompactBuffer) == 0 {
		return nil
	}

	excludeID := strings.TrimSpace(excludeMessageID)
	selected := make([]GroupCompactMessage, 0, min(limit, len(s.groupCompactBuffer)))
	totalChars := 0

	for i := len(s.groupCompactBuffer) - 1; i >= 0; i-- {
		item := s.groupCompactBuffer[i]
		if excludeID != "" && item.message.MessageID != "" && item.message.MessageID == excludeID {
			continue
		}
		if len(selected) >= limit {
			break
		}

		message := item.message
		displayLen := len([]rune(message.FormatDisplay()))
		if totalChars >= maxChars {
			break
		}
		if totalChars+displayLen > maxChars {
			if len(selected) == 0 {
				contentRunes := []rune(message.Content)
				if maxChars < len(contentRunes) {
					message.Content = string(contentRunes[:maxChars])
				}
				message.raw = ""
				selected = append(selected, message)
			}
			break
		}

		totalChars += displayLen
		selected = append(selected, message)
	}

	if len(selected) == 0 {
		return nil
	}
	result := make([]GroupCompactMessage, len(selected))
	for i, j := 0, len(selected)-1; j >= 0; i, j = i+1, j-1 {
		result[i] = selected[j]
	}
	return result
}

// recentPendingGroupMessagesLocked extracts uncompacted messages while s.mu is already held.
func (s *SessionContext) recentPendingGroupMessagesLocked(limit int, maxChars int, excludeMessageID string) []string {
	if limit <= 0 || maxChars <= 0 || len(s.groupCompactBuffer) == 0 {
		return nil
	}

	excludeID := strings.TrimSpace(excludeMessageID)
	var selected []string
	totalChars := 0

	// Traverse from newest to oldest to prioritize latest messages
	for i := len(s.groupCompactBuffer) - 1; i >= 0; i-- {
		item := s.groupCompactBuffer[i]

		if excludeID != "" && item.message.MessageID != "" && item.message.MessageID == excludeID {
			continue
		}

		if len(selected) >= limit {
			break
		}

		content := item.message.FormatDisplay()
		contentLen := len([]rune(content))

		if totalChars >= maxChars {
			break
		}

		if totalChars+contentLen > maxChars {
			if len(selected) == 0 {
				runes := []rune(content)
				if maxChars < len(runes) {
					content = string(runes[:maxChars])
				}
				selected = append(selected, content)
			}
			break
		}

		totalChars += contentLen
		selected = append(selected, content)
	}

	if len(selected) == 0 {
		return nil
	}

	// Reverse to restore chronological order (oldest -> newest)
	result := make([]string, len(selected))
	for i, j := 0, len(selected)-1; j >= 0; i, j = i+1, j-1 {
		result[i] = selected[j]
	}

	return result
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
	msgs := make([]GroupCompactMessage, len(s.groupCompactBuffer))
	msgIDs := make([]string, len(s.groupCompactBuffer))
	for i, item := range s.groupCompactBuffer {
		msgs[i] = item.message
		msgIDs[i] = item.message.MessageID
	}
	return GroupCompactSnapshot{
		Summary:         s.groupCompactSummary,
		Messages:        msgs,
		MessageIDs:      msgIDs,
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

	var firstID, lastID string
	if len(snapshot.MessageIDs) > 0 {
		firstID = snapshot.MessageIDs[0]
		lastID = snapshot.MessageIDs[len(snapshot.MessageIDs)-1]
	}
	formattedMsgs := make([]string, len(snapshot.Messages))
	for i, m := range snapshot.Messages {
		formattedMsgs[i] = m.FormatDisplay()
	}
	// Keep one bounded mapping for the latest compact batch. The summary is the
	// cumulative running summary after this batch, so callers must not present
	// this mapping as the complete set of all historical source messages.
	s.groupSummaryGroups = []SummaryGroup{
		{
			Summary:        summary,
			MessageIDs:     snapshot.MessageIDs,
			StartMessageID: firstID,
			EndMessageID:   lastID,
			Messages:       formattedMsgs,
			StructuredMsgs: snapshot.Messages,
		},
	}

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

// SummaryGroups returns a copy of the current summary groups mappings.
func (s *SessionContext) SummaryGroups() []SummaryGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.groupSummaryGroups) == 0 {
		return nil
	}
	groups := make([]SummaryGroup, len(s.groupSummaryGroups))
	copy(groups, s.groupSummaryGroups)
	return groups
}

// SetGroupRunningSummary sets the running summary for this session under the lock.
func (s *SessionContext) SetGroupRunningSummary(summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groupCompactSummary = summary
}

// ResetGroupCompact clears summary state and invalidates any in-flight result.
func (s *SessionContext) ResetGroupCompact() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.groupCompactGeneration++
	s.groupCompactSummary = ""
	s.groupCompactBuffer = nil
	s.groupSummaryGroups = nil
	s.UpdatedAt = time.Now()
}

// GroupCompactReady reports whether another full raw-message batch is ready.
func (s *SessionContext) GroupCompactReady(bufferSize int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bufferSize > 0 && len(s.groupCompactBuffer) >= bufferSize
}

// GroupCompactBufferCount returns the number of raw messages currently buffered.
func (s *SessionContext) GroupCompactBufferCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.groupCompactBuffer)
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
	s := strings.ToLower(sessionID)
	isGroupSession := strings.HasPrefix(s, "group:") || strings.Contains(s, ":group:")
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

	end := min(offset+limit, len(entries))

	result := make([]*SessionContext, 0, end-offset)
	for _, entry := range entries[offset:end] {
		result = append(result, entry.context)
	}
	return result
}
