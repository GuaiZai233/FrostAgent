//go:build !windows

package bridge

import "os"

func replaceStateFile(source, destination string) error { return os.Rename(source, destination) }
