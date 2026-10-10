package instance

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/mcp"
	"errors"
	"fmt"
	"maps"
)

func equalSettings(left, right map[string]string) bool { return maps.Equal(left, right) }

// reloadSQLRuntimeLocked applies persisted instance settings before the next
// request. The caller owns i.op, which drains writes while the runtime swaps.
func (m *Manager) reloadSQLRuntimeLocked(i *managed, enabled bool, previous map[string]string) error {
	if i.mcp != nil {
		if err := i.mcp.SetRuntimeActive(false); err != nil && !errors.Is(err, mcp.ErrManagerClosed) {
			i.logger.Warn(logs.SYSTEM, fmt.Sprintf("暂停 MCP 连接失败: %v", err))
		}
	}
	i.mu.RLock()
	old := i.runtime
	currentConfig := i.config
	i.mu.RUnlock()
	if old != nil {
		old.Stop()
	}
	r, c, err := m.buildFresh(i.id, i, enabled, m.dir(i.id))
	if err == nil && enabled && i.mcp != nil {
		err = i.mcp.SetRuntimeActive(true)
	}
	if err == nil {
		i.mu.Lock()
		i.runtime, i.config = r, c
		i.mu.Unlock()
		return nil
	}
	if r != nil {
		r.Stop()
	}
	applyErr := err
	current := currentConfig.Snapshot()
	for key := range current {
		if _, existed := previous[key]; !existed {
			applyErr = errors.Join(applyErr, currentConfig.Update(key, "", true))
		}
	}
	for key, value := range previous {
		if current[key] != value {
			applyErr = errors.Join(applyErr, currentConfig.Update(key, value, false))
		}
	}
	fallback, fallbackConfig, fallbackErr := m.buildFresh(i.id, i, enabled, m.dir(i.id))
	if fallbackErr == nil && enabled && i.mcp != nil {
		fallbackErr = i.mcp.SetRuntimeActive(true)
	}
	if fallbackErr != nil && fallback != nil {
		fallback.Stop()
		fallback = nil
	}
	i.mu.Lock()
	i.runtime, i.config = fallback, fallbackConfig
	i.mu.Unlock()
	if fallbackErr != nil {
		applyErr = errors.Join(applyErr, fallbackErr)
		_ = m.update(i.id, func(info *Info) { info.Enabled = false; info.Error = applyErr.Error() })
	}
	return applyErr
}
