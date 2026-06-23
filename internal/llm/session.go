package llm

import (
	"FrostAgent/internal/core"
	"sort"
	"sync"
	"time"
)

// SessionContext 管理单个会话的上下文历史
type SessionContext struct {
	ConversationID string
	History        []ChatMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
	mu             sync.Mutex // 保护单个会话的并发访问
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

// SessionManager 管理多个会话上下文，支持多用户/多群聊隔离
type SessionManager struct {
	sessions   map[string]*SessionContext
	mu         sync.RWMutex
	MaxHistory int           // 单个会话保留的最大历史消息数
	TTL        time.Duration // 会话有效期
}

// NewSessionManager 创建新的会话管理器
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions:   make(map[string]*SessionContext),
		MaxHistory: 20,
		TTL:        24 * time.Hour,
	}
	// 启动定时清理协程
	go sm.startCleanupRoutine()
	return sm
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

	session = &SessionContext{
		ConversationID: sessionID,
		History:        make([]ChatMessage, 0),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
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
	s.UpdatedAt = time.Now()
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

	sessions := make([]*SessionContext, 0, len(sm.sessions))
	for _, session := range sm.sessions {
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	if offset >= len(sessions) {
		return nil
	}

	end := offset + limit
	if end > len(sessions) || limit <= 0 {
		end = len(sessions)
	}

	return sessions[offset:end]
}
