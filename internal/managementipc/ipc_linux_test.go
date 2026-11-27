//go:build linux

package managementipc

import (
	"context"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/managementauth"
	"golang.org/x/sys/unix"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinuxEndpointOwnershipAndStaleSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "frostctl-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "management.sock")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	old.Close()
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Listen(path); err == nil {
		second.Close()
		t.Fatal("second service took live endpoint")
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("own socket was not removed")
	}
	if err = os.WriteFile(path, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if listener, err := Listen(path); err == nil {
		listener.Close()
		t.Fatal("replaced unrelated file")
	}
}
func TestLinuxRootPeerAndMissingSocket(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("UID 0 case runs explicitly in Linux CI")
	}
	path := filepath.Join(t.TempDir(), "management.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if conn, err := Dial(ctx, path); err == nil {
		conn.Close()
		t.Fatal("missing socket connected")
	}
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	server := Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !managementauth.LocalAdmin(r.Context()) {
			t.Error("missing local identity")
		}
		fmt.Fprint(w, "authorized")
	}))
	defer server.Close()
	go server.Serve(listener)
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return Dial(ctx, path) }}}
	defer c.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://local/api/version", nil)
	response, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
}
func TestLinuxNonRootCannotUseLocalClient(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root case runs in ordinary Linux CI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Dial(ctx, filepath.Join(t.TempDir(), "missing.sock"))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("client did not reject non-root identity first")
	}
}

type fakeRaw struct{ fail bool }

func (f fakeRaw) Control(fn func(uintptr)) error {
	if f.fail {
		return errors.New("control failed")
	}
	fn(1)
	return nil
}
func (fakeRaw) Read(func(uintptr) bool) error  { return nil }
func (fakeRaw) Write(func(uintptr) bool) error { return nil }
func TestPeerCredentialFailureIsDenied(t *testing.T) {
	if rootPeer(fakeRaw{}, func(int) (*unix.Ucred, error) { return &unix.Ucred{}, errors.New("getsockopt failed") }) {
		t.Fatal("zero-initialized UID trusted after lookup failure")
	}
	if rootPeer(fakeRaw{fail: true}, func(int) (*unix.Ucred, error) { return &unix.Ucred{}, nil }) {
		t.Fatal("Control failure trusted")
	}
	if rootPeer(fakeRaw{}, func(int) (*unix.Ucred, error) { return &unix.Ucred{Uid: 65534}, nil }) {
		t.Fatal("non-root peer admitted")
	}
	if !rootPeer(fakeRaw{}, func(int) (*unix.Ucred, error) { return &unix.Ucred{}, nil }) {
		t.Fatal("root peer rejected")
	}
}
