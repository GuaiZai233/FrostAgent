//go:build !windows

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicReplaceFile atomically replaces destination with source on POSIX platforms
// using rename(2), followed by fsync on the parent directory to ensure durability.
func atomicReplaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}

	// Fsync parent directory for POSIX crash-durability
	dir := filepath.Dir(destination)
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
