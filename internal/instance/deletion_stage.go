package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const deletionStagePrefix = ".frostagent-delete-"

func (m *Manager) createDeletionStage(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("invalid instance ID")
	}
	return os.MkdirTemp(m.root, deletionStagePrefix+id+"-")
}

func (m *Manager) removeDeletionStage(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(m.root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.Dir(rel) != "." ||
		!strings.HasPrefix(rel, deletionStagePrefix) {
		return fmt.Errorf("deletion stage escapes data root: %s", path)
	}
	if err := safeTree(abs); err != nil {
		return err
	}
	return os.RemoveAll(abs)
}

// recoverDeletionStages restores a pending instance directory after an
// interrupted SQL delete, or cleans files whose SQL deletion committed.
func (m *Manager) recoverDeletionStages() error {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(m.registry.Instances))
	for _, info := range m.registry.Instances {
		known[info.ID] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, deletionStagePrefix) {
			continue
		}
		rest := strings.TrimPrefix(name, deletionStagePrefix)
		if len(rest) < 10 || rest[8] != '-' || !idPattern.MatchString(rest[:8]) {
			continue
		}
		id := rest[:8]
		stage := filepath.Join(m.root, name)
		if err := safeTree(stage); err != nil {
			return err
		}
		stagedInstance := filepath.Join(stage, "instance")
		if known[id] {
			if _, err := os.Lstat(stagedInstance); err == nil {
				if _, err := os.Lstat(m.dir(id)); !os.IsNotExist(err) {
					return fmt.Errorf("cannot restore staged instance %s: destination exists or is inaccessible", id)
				}
				if err := os.Rename(stagedInstance, m.dir(id)); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if err := m.removeDeletionStage(stage); err != nil {
			return err
		}
	}
	return nil
}
