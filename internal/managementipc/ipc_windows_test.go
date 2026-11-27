//go:build windows

package managementipc

import (
	"context"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/managementauth"
	"golang.org/x/sys/windows"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestWindowsLocalOnlyPipeNames(t *testing.T) {
	for _, path := range []string{`\\remote\pipe\FrostAgent.Management`, `\\.\pipe\nested\endpoint`, "pipe"} {
		if localPipe(path) {
			t.Fatalf("accepted nonlocal or malformed pipe %q", path)
		}
	}
	if !localPipe(defaultEndpoint) {
		t.Fatal("default pipe rejected")
	}
}
func TestWindowsElevationAndEndpointExclusion(t *testing.T) {
	path := fmt.Sprintf(`\\.\pipe\FrostAgent.Test.%d`, os.Getpid())
	if !administrator(windows.GetCurrentProcessToken()) {
		if listener, err := Listen(path); err == nil {
			listener.Close()
			t.Fatal("unelevated standalone backend was trusted")
		}
		if conn, err := Dial(context.Background(), path); err == nil {
			conn.Close()
			t.Fatal("unelevated client was admitted")
		}
		return
	}
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Listen(path); err == nil {
		other.Close()
		t.Fatal("second server took endpoint")
	}
	server := Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !managementauth.LocalAdmin(r.Context()) {
			t.Error("local identity missing")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	go server.Serve(listener)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}
