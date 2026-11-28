//go:build windows

package frostctl

import (
	"context"
	"errors"
	"github.com/danieljoos/wincred"
	"golang.org/x/sys/windows"
	"os"
	"time"
)

func privatePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return errors.New("configuration path must not be a link")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
func replaceFile(from, to string) error {
	a, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func lockConfig(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = privatePath(path); err != nil {
		f.Close()
		return nil, err
	}
	overlapped := &windows.Overlapped{}
	for {
		err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
		if err == nil {
			return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped); f.Close() }, nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
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
func writeSystemCredential(_ context.Context, id string, data []byte) error {
	c := wincred.NewGenericCredential("FrostAgent/frostctl/" + id)
	c.CredentialBlob = data
	return c.Write()
}
func readSystemCredential(_ context.Context, id string) ([]byte, error) {
	c, err := wincred.GetGenericCredential("FrostAgent/frostctl/" + id)
	if err != nil {
		return nil, err
	}
	return c.CredentialBlob, nil
}
func deleteSystemCredential(_ context.Context, id string) error {
	c, err := wincred.GetGenericCredential("FrostAgent/frostctl/" + id)
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.Delete()
}
