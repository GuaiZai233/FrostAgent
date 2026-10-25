//go:build !windows

package bridge

import (
	"errors"
	"os"
)

func syncDataDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// Link publishes the already-synced inode without clobbering an existing log.
// The caller synchronizes the parent directory before admitting any operation.
func publishAuditFile(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	return os.Remove(source)
}
