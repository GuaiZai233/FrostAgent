package bridge

import (
	"errors"
	"os"
	"path/filepath"
)

// WriteStateAtomic persists VM metadata, never raw tool content.
func WriteStateAtomic(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err := replaceStateFile(file.Name(), path); err != nil {
		return err
	}
	return syncDataDirectory(filepath.Dir(path))
}
