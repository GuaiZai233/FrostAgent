package memory

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ReflectionStatus describes the single background reflection job.
type ReflectionStatus struct {
	Running         bool
	Owner           string
	StartedAt       time.Time
	LastCompletedAt time.Time
	LastError       string
}

// ReflectionManager starts reflection in the background and prevents
// overlapping reflection jobs.
type ReflectionManager struct {
	*runtimescope.Scope
	reflector *Reflector
	mu        sync.RWMutex
	status    ReflectionStatus
}

// NewReflectionManager creates a background reflection coordinator.
func NewReflectionManager(reflector *Reflector) *ReflectionManager {
	return &ReflectionManager{reflector: reflector}
}

// Start launches reflection and returns immediately. An empty owner means all
// owners; otherwise only that owner's memories are processed.
func (m *ReflectionManager) Start(owner string) (ReflectionStatus, bool, error) {
	if m == nil || m.reflector == nil || !m.reflector.Available() {
		return ReflectionStatus{}, false, fmt.Errorf("memory reflection is not configured")
	}

	owner = strings.TrimSpace(owner)
	m.mu.Lock()
	if m.status.Running {
		status := m.status
		m.mu.Unlock()
		return status, false, nil
	}
	m.status.Running = true
	m.status.Owner = owner
	m.status.StartedAt = time.Now()
	m.status.LastError = ""
	status := m.status
	m.mu.Unlock()

	if !m.Go(func() { m.run(owner) }) {
		m.mu.Lock()
		m.status.Running = false
		m.mu.Unlock()
		return status, false, fmt.Errorf("实例未启用")
	}
	return status, true, nil
}

// Status returns a snapshot of the current or most recent job.
func (m *ReflectionManager) Status() ReflectionStatus {
	if m == nil {
		return ReflectionStatus{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *ReflectionManager) run(owner string) {
	ctx := m.Context()
	var err error
	if owner == "" {
		err = m.reflector.Reflect(ctx)
	} else {
		err = m.reflector.ReflectOwner(ctx, owner)
	}

	m.mu.Lock()
	m.status.Running = false
	m.status.LastCompletedAt = time.Now()
	if err != nil {
		m.status.LastError = err.Error()
	} else {
		m.status.LastError = ""
	}
	m.mu.Unlock()

	if err != nil {
		m.Log().Error(logs.SYSTEM, fmt.Sprintf("后台记忆反思失败: %v", err))
		return
	}
	m.Log().Info(logs.SYSTEM, "后台记忆反思任务已完成")
}
