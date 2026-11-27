//go:build !linux && !windows

package frostctl

import (
	"context"
	"errors"
	"os"
)

func privatePath(path string) error     { return os.Chmod(path, 0600) }
func replaceFile(from, to string) error { return os.Rename(from, to) }
func lockConfig(context.Context, string) (func(), error) {
	return nil, errors.New("remote configuration supported on Linux and Windows")
}
func writeSystemCredential(context.Context, string, []byte) error { return errors.New("unavailable") }
func readSystemCredential(context.Context, string) ([]byte, error) {
	return nil, errors.New("unavailable")
}
func deleteSystemCredential(context.Context, string) error { return errors.New("unavailable") }
