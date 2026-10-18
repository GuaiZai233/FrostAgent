package instance

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/mcp"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/security"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"
)

// SettingsWriteHandler serializes other global setting writers with draft
// commit, runtime application and rollback. Reads do not need this lock.
func (m *Manager) SettingsWriteHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			m.settingsWriteMu.Lock()
			defer m.settingsWriteMu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

func equalSettings(left, right map[string]string) bool { return maps.Equal(left, right) }

func (m *Manager) restoreGlobalSnapshot(previous map[string]string) error {
	keys := maps.Clone(instanceconfig.GlobalKeys)
	for key := range instanceconfig.SharedKeys {
		keys[key] = true
	}
	return m.global.ReplaceDatabaseSubset(keys, previous)
}

func (m *Manager) rollbackSettingsDraft(i *managed, before, after, globalBefore, globalAfter map[string]string, enabled, reload bool) error {
	i.op.Lock()
	i.mu.RLock()
	config := i.config
	i.mu.RUnlock()
	var changes []instanceconfig.DatabaseChange
	appendChanges := func(store *instanceconfig.Store, previous, committed map[string]string) {
		keys := make(map[string]bool)
		for key := range previous {
			keys[key] = true
		}
		for key := range committed {
			keys[key] = true
		}
		for key := range keys {
			value, existed := previous[key]
			current, exists := committed[key]
			if value != current || existed != exists {
				changes = append(changes, instanceconfig.DatabaseChange{Store: store, Key: key, Value: value, Clear: !existed})
			}
		}
	}
	appendChanges(config, before, after)
	appendChanges(m.global, globalBefore, globalAfter)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := instanceconfig.ApplyDatabaseChanges(ctx, changes)
	cancel()
	if err == nil && reload {
		err = m.reloadSQLRuntimeLocked(i, enabled, before)
	}
	i.op.Unlock()
	if err == nil {
		err = m.applyGlobalSettings()
	}
	return err
}

// ApplyGlobalSettings refreshes control-plane dependencies and every instance
// runtime after a committed global setting change. Pending deletions stay paused.
func (m *Manager) ApplyGlobalSettings() error {
	if m.db == nil {
		return fmt.Errorf("global hot apply requires SQL storage")
	}
	m.globalApplyMu.Lock()
	defer m.globalApplyMu.Unlock()
	if m.shutdown.Err() != nil {
		return ErrClosing
	}
	sandboxCfg := sandbox.LoadConfig(m.global.Get)
	if sandboxCfg.Enabled {
		if err := sandboxCfg.Validate(); err != nil {
			return err
		}
	}
	billingCfg := billing.LoadConfig(m.global.Get)
	baseURL := billingCfg.BaseURL
	if baseURL == "" {
		baseURL = billing.DefaultAlcyoneBaseURL
	}
	m.billing.Store(billing.NewClient(baseURL, billingCfg.ServiceToken, billingCfg.Timeout))
	m.sandbox.ApplySnapshot(sandboxCfg)
	rawTimeout := strings.TrimSpace(m.global.Get("SECURITY_GATEWAY_TIMEOUT"))
	if rawTimeout == "" {
		rawTimeout = strings.TrimSpace(m.global.Get("SECURITY_CLASSIFIER_TIMEOUT"))
	}
	m.security.SetClassifierTimeout(security.ValidateClassifierTimeout(rawTimeout, security.DefaultClassifierTimeout))
	m.security.SetMode(security.ParseControlMode(m.global.Get("SECURITY_CONTROL_MODE")))
	currentGlobal := m.global.Snapshot()
	if !needsGlobalRuntimeReload(m.appliedGlobal, currentGlobal) {
		m.appliedGlobal = currentGlobal
		return nil
	}

	items, _ := m.List()
	var applyErr error
	for _, info := range items {
		if info.Deleting {
			continue
		}
		i, err := m.lookup(info.ID)
		if err != nil {
			applyErr = errors.Join(applyErr, err)
			continue
		}
		i.op.Lock()
		if m.shutdown.Err() != nil {
			i.op.Unlock()
			return ErrClosing
		}
		i.mu.RLock()
		config := i.config
		i.mu.RUnlock()
		if config != nil {
			err = m.reloadSQLRuntimeLocked(i, info.Enabled, config.Snapshot())
			applyErr = errors.Join(applyErr, err)
		}
		i.op.Unlock()
	}
	if applyErr == nil {
		m.appliedGlobal = currentGlobal
	}
	return applyErr
}

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
		if old != nil && old.Engine != nil && r != nil && r.Engine != nil {
			r.Engine.GroupManager.CarrySQLStoresFrom(old.Engine.GroupManager)
			old.Engine.SessionManager.TransferSessionsTo(r.Engine.SessionManager)
		}
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
	if fallbackErr == nil && old != nil && old.Engine != nil && fallback != nil && fallback.Engine != nil {
		fallback.Engine.GroupManager.CarrySQLStoresFrom(old.Engine.GroupManager)
		old.Engine.SessionManager.TransferSessionsTo(fallback.Engine.SessionManager)
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
