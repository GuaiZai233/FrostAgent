//go:build windows

package security

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// atomicReplaceFile replaces destination without deleting the existing access
// state first, using the same write-through replacement as the MCP store.
func atomicReplaceFile(source, destination string) error {
	srcPtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return fmt.Errorf("invalid source path %q: %w", source, err)
	}
	dstPtr, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return fmt.Errorf("invalid destination path %q: %w", destination, err)
	}

	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING | windows.MOVEFILE_WRITE_THROUGH)
	if err := windows.MoveFileEx(srcPtr, dstPtr, flags); err != nil {
		return fmt.Errorf("windows MoveFileEx failed: %w", err)
	}
	return nil
}
