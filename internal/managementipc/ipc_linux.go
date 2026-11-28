//go:build linux

package managementipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const defaultEndpoint = "/run/frostagent/management.sock"

type listener struct {
	*net.UnixListener
	path string
	info os.FileInfo
	lock *os.File
	once sync.Once
}

func protectedDirectory(path string, create bool) error {
	if !filepath.IsAbs(path) {
		return errors.New("IPC path must be absolute")
	}
	dir := filepath.Dir(path)
	if create {
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	immediateUID := uint32(0)
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		uid := info.Sys().(*syscall.Stat_t).Uid
		if current == dir {
			immediateUID = uid
		}
		if current != dir && uid != 0 {
			return fmt.Errorf("IPC ancestor is not root-owned: %s", current)
		}
		if current == dir && create && uid != 0 && uid != uint32(os.Geteuid()) {
			return errors.New("IPC directory belongs to another account")
		}
		writable := info.Mode()&0022 != 0
		stickyRoot := info.Mode()&os.ModeSticky != 0 && uid == 0
		if !info.IsDir() || writable && !stickyRoot {
			return fmt.Errorf("unsafe IPC directory: %s", current)
		}
		if !create && stickyRoot && writable && immediateUID != 0 {
			return errors.New("IPC service directory below a shared writable ancestor must be root-owned")
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	return nil
}
func Listen(path string) (net.Listener, error) {
	if err := protectedDirectory(path, true); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	owner := info.Sys().(*syscall.Stat_t).Uid
	if owner != 0 && owner != uint32(os.Geteuid()) {
		return nil, errors.New("IPC directory is owned by another account")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("local management endpoint is already in use")
	}
	fail := func(err error) (net.Listener, error) { lock.Close(); return nil, err }
	if old, err := os.Lstat(path); err == nil {
		if old.Mode()&os.ModeSocket == 0 || old.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return fail(errors.New("refusing to replace an unowned IPC endpoint"))
		}
		conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return fail(errors.New("local management endpoint is active"))
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return fail(dialErr)
		}
		if err := os.Remove(path); err != nil {
			return fail(err)
		}
	} else if !os.IsNotExist(err) {
		return fail(err)
	}
	unixListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fail(err)
	}
	unixListener.SetUnlinkOnClose(false)
	info, err = os.Lstat(path)
	if err != nil {
		unixListener.Close()
		return fail(err)
	}
	l := &listener{UnixListener: unixListener, path: path, info: info, lock: lock}
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
func (l *listener) Accept() (net.Conn, error) {
	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		raw, err := conn.SyscallConn()
		if err != nil || !rootPeer(raw, func(fd int) (*unix.Ucred, error) { return unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED) }) {
			conn.Close()
			continue
		}
		return conn, nil
	}
}
func (l *listener) Close() error {
	var err error
	l.once.Do(func() {
		err = l.UnixListener.Close()
		if current, statErr := os.Lstat(l.path); statErr == nil && os.SameFile(current, l.info) {
			err = errors.Join(err, os.Remove(l.path))
		}
		err = errors.Join(err, l.lock.Close())
	})
	return err
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("local management requires UID 0")
	}
	if err := protectedDirectory(path, false); err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

func rootPeer(raw syscall.RawConn, lookup func(int) (*unix.Ucred, error)) bool {
	var identity *unix.Ucred
	var identityErr error
	controlErr := raw.Control(func(fd uintptr) { identity, identityErr = lookup(int(fd)) })
	return controlErr == nil && identityErr == nil && identity != nil && identity.Uid == 0
}
