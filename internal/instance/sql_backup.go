package instance

import (
	"FrostAgent/internal/backup"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
