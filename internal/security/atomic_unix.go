//go:build !windows

package security

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicReplaceFile atomically replaces destination using rename, then syncs
// its parent directory on a best-effort basis, matching the MCP store.
func atomicReplaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}

	if dir, err := os.Open(filepath.Dir(destination)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
