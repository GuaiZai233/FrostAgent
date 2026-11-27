//go:build linux

package frostctl

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func privatePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if (!info.IsDir() && !info.Mode().IsRegular()) || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return errors.New("configuration path is not owned by the current user")
	}
	mode := os.FileMode(0600)
	if info.IsDir() {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
func replaceFile(from, to string) error { return os.Rename(from, to) }
func lockConfig(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return func() { f.Close() }, nil
		}
		if err != unix.EWOULDBLOCK {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func writeSystemCredential(ctx context.Context, id string, data []byte) error {
	cmd := exec.CommandContext(ctx, "secret-tool", "store", "--label=FrostAgent remote", "application", "frostctl", "credential", id)
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}
func readSystemCredential(ctx context.Context, id string) ([]byte, error) {
	return exec.CommandContext(ctx, "secret-tool", "lookup", "application", "frostctl", "credential", id).Output()
}
func deleteSystemCredential(ctx context.Context, id string) error {
	return exec.CommandContext(ctx, "secret-tool", "clear", "application", "frostctl", "credential", id).Run()
}
