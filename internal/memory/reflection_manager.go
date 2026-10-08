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
// Optional onComplete callbacks are invoked upon task completion with the result error.
func (m *ReflectionManager) Start(owner string, onComplete ...func(err error)) (ReflectionStatus, bool, error) {
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

	if !m.Go(func() { m.run(owner, onComplete...) }) {
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

func (m *ReflectionManager) run(owner string, onComplete ...func(err error)) {
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

	for _, cb := range onComplete {
		if cb != nil {
			cb(err)
		}
	}

	if err != nil {
		m.Log().Error(logs.SYSTEM, fmt.Sprintf("后台记忆反思失败: %v", err))
		return
	}
	m.Log().InfoWithConsoleSummary(logs.SYSTEM, "后台记忆反思任务已完成", "后台记忆反思任务已完成")
}

// StartGroup launches reflection for an isolated group store in the background.
func (m *ReflectionManager) StartGroup(groupStore *GroupStore, onComplete ...func(err error)) (ReflectionStatus, bool, error) {
	if m == nil || m.reflector == nil || !m.reflector.Available() {
		return ReflectionStatus{}, false, fmt.Errorf("memory reflection is not configured")
	}
	if groupStore == nil {
		return ReflectionStatus{}, false, fmt.Errorf("group store is required")
	}

	groupID := groupStore.GroupID()
	ownerLabel := "group:" + groupID

	m.mu.Lock()
	if m.status.Running {
		status := m.status
		m.mu.Unlock()
		return status, false, nil
	}
	m.status.Running = true
	m.status.Owner = ownerLabel
	m.status.StartedAt = time.Now()
	m.status.LastError = ""
	status := m.status
	m.mu.Unlock()

	if !m.Go(func() { m.runGroup(groupStore, onComplete...) }) {
		m.mu.Lock()
		m.status.Running = false
		m.mu.Unlock()
		return status, false, fmt.Errorf("实例未启用")
	}
	return status, true, nil
}

func (m *ReflectionManager) runGroup(groupStore *GroupStore, onComplete ...func(err error)) {
	ctx := m.Context()
	err := m.reflector.ReflectGroup(ctx, groupStore)

	m.mu.Lock()
	m.status.Running = false
	m.status.LastCompletedAt = time.Now()
	if err != nil {
		m.status.LastError = err.Error()
	} else {
		m.status.LastError = ""
	}
	m.mu.Unlock()

	for _, cb := range onComplete {
		if cb != nil {
			cb(err)
		}
	}

	if err != nil {
		m.Log().Error(logs.SYSTEM, fmt.Sprintf("后台群记忆反思失败 (群: %s): %v", groupStore.GroupID(), err))
		return
	}
	m.Log().InfoWithConsoleSummary(logs.SYSTEM, fmt.Sprintf("后台群记忆反思任务已完成 (群: %s)", groupStore.GroupID()), "后台群记忆反思任务已完成")
}
