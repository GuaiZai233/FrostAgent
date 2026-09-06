//go:build windows

package mcp

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// atomicReplaceFile atomically replaces destination with source on Windows
// using the Win32 MoveFileExW API with MOVEFILE_REPLACE_EXISTING and MOVEFILE_WRITE_THROUGH.
// This guarantees that the destination file is replaced atomically at the NTFS/filesystem
// level without any deletion window where data could be lost on sudden power failure.
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
