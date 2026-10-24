//go:build windows

package bridge

import "golang.org/x/sys/windows"

// Windows has no POSIX directory fsync. Newly published audit files use a
// write-through move below; appends use File.Sync (FlushFileBuffers).
func syncDataDirectory(string) error { return nil }

func publishAuditFile(source, destination string) error {
	src, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	// Intentionally omit REPLACE_EXISTING: a concurrent external writer must
	// cause admission failure, not silent replacement of its audit history.
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_WRITE_THROUGH)
}
