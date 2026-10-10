package instance

import (
	"FrostAgent/internal/backup"
	"FrostAgent/internal/logs"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

func (m *Manager) InstanceZIP(id string) ([]byte, error) {
	if m.db == nil {
		return nil, fmt.Errorf("instance ZIP requires SQL storage")
	}
	i, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	if !i.op.TryRLock() {
		return nil, ErrBusy
	}
	defer i.op.RUnlock()
	if m.shutdown.Err() != nil {
		return nil, ErrClosing
	}
	if err := safeTree(m.dir(i.id)); err != nil {
		return nil, err
	}
	return backup.BuildInstanceZIP(m.db, i.id, filepath.Join(m.dir(i.id), "sticker"))
}

func (m *Manager) InstancePart(id, part string) (data []byte, mediaType, fileName string, err error) {
	if m.db == nil {
		return nil, "", "", fmt.Errorf("instance export requires SQL storage")
	}
	i, err := m.lookup(id)
	if err != nil {
		return nil, "", "", err
	}
	if !i.op.TryRLock() {
		return nil, "", "", ErrBusy
	}
	defer i.op.RUnlock()
	if m.shutdown.Err() != nil {
		return nil, "", "", ErrClosing
	}
	var value any
	switch part {
	case "settings":
		value, err = backup.ExportSettings(m.db, i.id)
		fileName = "setting.json"
	case "memories":
		value, err = backup.ExportMemories(m.db, i.id)
		fileName = "memory.json"
	case "summaries":
		value, err = backup.ExportSummaries(m.db, i.id)
		fileName = "group_summaries.json"
	case "stickers":
		data, err = backup.BuildStickerZIP(m.db, i.id, filepath.Join(m.dir(i.id), "sticker"))
		return data, "application/zip", "stickers.zip", err
	default:
		return nil, "", "", fs.ErrNotExist
	}
	if err != nil {
		return nil, "", "", err
	}
	data, err = json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, "", "", err
	}
	return append(data, '\n'), "application/json", fileName, nil
}

func (m *Manager) ImportSettings(id string, data backup.Settings) error {
	if m.db == nil {
		return fmt.Errorf("setting import requires SQL storage")
	}
	i, err := m.lookup(id)
	if err != nil {
		return err
	}
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if err := m.rejectDeleting(i.id); err != nil {
		return err
	}
	list, _ := m.List()
	enabled := false
	for _, info := range list {
		if info.ID == i.id {
			enabled = info.Enabled
			break
		}
	}
	i.mu.Lock()
	oldRuntime, oldMCP := i.runtime, i.mcp
	i.runtime, i.mcp = nil, nil
	i.mu.Unlock()
	if oldRuntime != nil {
		oldRuntime.Stop()
	}
	if oldMCP != nil {
		_ = oldMCP.Close()
	}
	importErr := backup.ImportSettings(m.db, i.id, data)
	if importErr == nil {
		owners, err := m.db.LoadEndpointOwners(context.Background())
		if err != nil {
			importErr = err
		} else {
			m.endpointMu.Lock()
			m.endpointOwners = owners
			m.endpointMu.Unlock()
		}
	}
	r, c, buildErr := m.buildFresh(i.id, i, enabled, m.dir(i.id))
	if buildErr == nil && enabled && i.mcp != nil {
		buildErr = i.mcp.SetRuntimeActive(true)
	}
	if buildErr != nil && r != nil {
		r.Stop()
		r = nil
	}
	i.mu.Lock()
	i.runtime, i.config = r, c
	i.mu.Unlock()
	if buildErr != nil {
		_ = m.update(i.id, func(info *Info) { info.Enabled = false; info.Error = buildErr.Error() })
		i.logger.Error(logs.SYSTEM, fmt.Sprintf("导入设置后重建实例失败: %v", buildErr))
	}
	return errors.Join(importErr, buildErr)
}

func (m *Manager) handleSettingsImport(w http.ResponseWriter, r *http.Request, id string) {
	var data backup.Settings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	if err := decoder.Decode(&data); err != nil {
		writeError(w, err)
		return
	}
	if err := m.ImportSettings(id, data); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (m *Manager) PrepareDeletion(id string) error {
	if m.db == nil {
		return fmt.Errorf("pending deletion requires SQL storage")
	}
	i, err := m.lookup(id)
	if err != nil {
		return err
	}
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if m.shutdown.Err() != nil {
		return ErrClosing
	}
	if err := m.rejectDeleting(i.id); err != nil {
		return err
	}
	if err := m.stop(i.id, i); err != nil {
		return err
	}
	if err := m.db.PrepareDeletion(context.Background(), i.id, backup.FormatVersion); err != nil {
		return err
	}
	m.mu.Lock()
	for n := range m.registry.Instances {
		if m.registry.Instances[n].ID == i.id {
			m.registry.Instances[n].Deleting = true
			m.registry.Instances[n].Enabled = false
			break
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) CancelDeletion(id string) error {
	if m.db == nil {
		return fmt.Errorf("pending deletion requires SQL storage")
	}
	i, err := m.lookup(id)
	if err != nil {
		return err
	}
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if m.shutdown.Err() != nil {
		return ErrClosing
	}
	if err := m.db.CancelDeletion(context.Background(), i.id); err != nil {
		return err
	}
	m.mu.Lock()
	for n := range m.registry.Instances {
		if m.registry.Instances[n].ID == i.id {
			m.registry.Instances[n].Deleting = false
			m.registry.Instances[n].Enabled = false
			break
		}
	}
	m.mu.Unlock()
	r, c, err := m.buildFresh(i.id, i, false, m.dir(i.id))
	i.mu.Lock()
	i.runtime, i.config = r, c
	i.mu.Unlock()
	if err != nil {
		_ = m.update(i.id, func(info *Info) { info.Error = err.Error() })
	}
	return err
}

func (m *Manager) ConfirmDeletion(id string) error {
	if m.db == nil {
		return fmt.Errorf("pending deletion requires SQL storage")
	}
	i, err := m.lookup(id)
	if err != nil {
		return err
	}
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if m.shutdown.Err() != nil {
		return ErrClosing
	}
	m.mu.RLock()
	pending := false
	for _, info := range m.registry.Instances {
		if info.ID == i.id {
			pending = info.Deleting
			break
		}
	}
	m.mu.RUnlock()
	if !pending {
		return errors.New("instance deletion has not been prepared")
	}
	if err := safeTree(m.dir(i.id)); err != nil {
		return err
	}
	if i.mcp != nil {
		if err := i.mcp.Close(); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(m.dir(i.id)); err != nil {
		return err
	}
	if err := m.db.ConfirmDeletion(context.Background(), i.id); err != nil {
		return err
	}
	m.mu.Lock()
	for n, info := range m.registry.Instances {
		if info.ID == i.id {
			m.registry.Instances = append(m.registry.Instances[:n], m.registry.Instances[n+1:]...)
			break
		}
	}
	delete(m.instances, i.id)
	m.mu.Unlock()
	m.endpointMu.Lock()
	for endpointID, owner := range m.endpointOwners {
		if owner == i.id {
			delete(m.endpointOwners, endpointID)
		}
	}
	m.endpointMu.Unlock()
	if m.security != nil {
		m.security.RemoveInstanceProvider(i.id)
	}
	return nil
}
