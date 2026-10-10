package instance

import (
	"FrostAgent/internal/backup"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/mcp"
	"FrostAgent/internal/memory"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func (m *Manager) ImportMemories(id string, data backup.Memories) (memory.ImportResult, error) {
	if m.db == nil {
		return memory.ImportResult{}, fmt.Errorf("memory import requires SQL storage")
	}
	i, err := m.lookup(id)
	if err != nil {
		return memory.ImportResult{}, err
	}
	if !i.op.TryLock() {
		return memory.ImportResult{}, ErrBusy
	}
	defer i.op.Unlock()
	if err := m.rejectDeleting(i.id); err != nil {
		return memory.ImportResult{}, err
	}
	i.mu.RLock()
	runtime, config := i.runtime, i.config
	i.mu.RUnlock()
	if runtime == nil || config == nil {
		return memory.ImportResult{}, fmt.Errorf("instance runtime is unavailable")
	}
	enabled := runtime.Scope.Context().Err() == nil
	if i.mcp != nil {
		if err := i.mcp.SetRuntimeActive(false); err != nil && !errors.Is(err, mcp.ErrManagerClosed) {
			return memory.ImportResult{}, err
		}
	}
	runtime.Stop()
	result, importErr := backup.ImportMemories(m.db, i.id, data, true)
	rebuildErr := m.reloadSQLRuntimeLocked(i, enabled, config.Snapshot())
	return result, errors.Join(importErr, rebuildErr)
}

func (m *Manager) handleMemoryImport(w http.ResponseWriter, r *http.Request, id string) {
	var data backup.Memories
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20))
	if err := decoder.Decode(&data); err != nil {
		writeError(w, err)
		return
	}
	result, err := m.ImportMemories(id, data)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"success": true, "imported": result.Imported,
		"skipped": result.Skipped,
		"warning": "不同 ID 的相同内容可能重复，请在前端页面人工查重。"})
}

func (m *Manager) RestoreInstanceZIP(name string, archive []byte) (result Info, resultErr error) {
	if m.db == nil {
		return Info{}, fmt.Errorf("instance restore requires SQL storage")
	}
	decoded, err := backup.DecodeInstanceZIP(archive)
	if err != nil {
		return Info{}, err
	}
	info, err := m.Create(name)
	if err != nil {
		return Info{}, err
	}
	defer func() {
		if resultErr != nil {
			prepareErr := m.PrepareDeletion(info.ID)
			if prepareErr == nil {
				prepareErr = m.ConfirmDeletion(info.ID)
			}
			resultErr = errors.Join(resultErr, prepareErr)
		}
	}()
	if info.Error != "" {
		return Info{}, fmt.Errorf("create restore target: %s", info.Error)
	}
	i, err := m.lookup(info.ID)
	if err != nil {
		return Info{}, err
	}
	i.op.Lock()
	defer i.op.Unlock()
	if err := backup.RemapEndpointIDsForInstance(&decoded.Settings, info.ID); err != nil {
		return Info{}, err
	}
	if err := backup.ImportSettings(m.db, info.ID, decoded.Settings); err != nil {
		return Info{}, err
	}
	if _, err := backup.ImportMemories(m.db, info.ID, decoded.Memories, false); err != nil {
		return Info{}, err
	}
	if err := backup.ImportSummaries(m.db, info.ID, decoded.Summaries); err != nil {
		return Info{}, err
	}
	if err := backup.ImportStickers(m.db, info.ID, filepath.Join(m.dir(info.ID), "sticker"),
		decoded.Stickers, decoded.Images); err != nil {
		return Info{}, err
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
	runtime, config, err := m.buildFresh(info.ID, i, false, m.dir(info.ID))
	if err != nil {
		if runtime != nil {
			runtime.Stop()
		}
		return Info{}, err
	}
	i.mu.Lock()
	i.runtime, i.config = runtime, config
	i.mu.Unlock()
	owners, err := m.db.LoadEndpointOwners(context.Background())
	if err != nil {
		return Info{}, err
	}
	m.endpointMu.Lock()
	m.endpointOwners = owners
	m.endpointMu.Unlock()
	return info, nil
}

func (m *Manager) handleInstanceRestore(w http.ResponseWriter, r *http.Request) {
	archive, err := io.ReadAll(http.MaxBytesReader(w, r.Body, backup.MaxInstanceZIPBytes))
	if err != nil {
		writeError(w, err)
		return
	}
	info, err := m.RestoreInstanceZIP(r.URL.Query().Get("name"), archive)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, info)
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
