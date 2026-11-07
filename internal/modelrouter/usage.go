package modelrouter

import (
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/tokenusage"
)

// SetUsageStore is called before any runtime background workers are started.
func (m *Manager) SetUsageStore(store *tokenusage.Store) {
	m.mu.Lock()
	m.usage = store
	m.mu.Unlock()
}
func (m *Manager) usageObserver(module string) func(*core.Usage) {
	m.mu.RLock()
	store := m.usage
	m.mu.RUnlock()
	if store == nil {
		return nil
	}
	return func(usage *core.Usage) {
		if err := store.Record(module, usage); err != nil {
			m.Log().Error(logs.SYSTEM, "Token 用量保存失败，统计可能不完整: "+err.Error())
		}
	}
}
