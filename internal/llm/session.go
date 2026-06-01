package llm

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// SessionContext 管理单个会话的上下文历史
type SessionContext struct {
	ConversationID string
	Messages       []ChatMessage
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
		Messages:       make([]ChatMessage, 0),
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
		if now.Sub(s.UpdatedAt) > sm.TTL {
			delete(sm.sessions, id)
		}
	}
}
