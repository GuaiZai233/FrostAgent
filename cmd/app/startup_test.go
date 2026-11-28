package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func TestStartupContinuesWithoutIPCAndRequiresHTTPToken(t *testing.T) {
	for _, token := range []string{"", "synthetic-bootstrap-control-token-32-chars"} {
		t.Run(map[bool]string{true: "bootstrap", false: "unconfigured"}[token != ""], func(t *testing.T) {
			previousLogger := logs.General
			logs.General = logs.New("", "General", 100)
			t.Cleanup(func() { logs.General = previousLogger })
			root, unrelated := t.TempDir(), t.TempDir()
			t.Chdir(unrelated)
			t.Setenv(dockerBootstrapTokenKey, token)
			probe, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := probe.Addr().String()
			probe.Close()
			oldListen, oldWS := instanceconfig.DefaultListenAddr, instanceconfig.DefaultWSListenAddr
			instanceconfig.DefaultListenAddr, instanceconfig.DefaultWSListenAddr = address, address
			t.Cleanup(func() { instanceconfig.DefaultListenAddr, instanceconfig.DefaultWSListenAddr = oldListen, oldWS })
			ctx, cancel := context.WithCancel(context.Background())
			finished := make(chan error, 1)
			go func() {
				finished <- runWithContext(ctx, root, func(string) (net.Listener, error) {
					return nil, errors.New("synthetic IPC permission denied")
				})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-finished:
					if err != nil {
						t.Errorf("shutdown: %v", err)
					}
				case <-time.After(15 * time.Second):
					t.Error("backend did not shut down")
				}
			})
			client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}}
			t.Cleanup(client.CloseIdleConnections)
			url := "http://" + address + "/api/version"
			deadline := time.Now().Add(5 * time.Second)
			for {
				response, err := client.Get(url)
				if err == nil {
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if response.StatusCode != http.StatusUnauthorized {
						t.Fatalf("tokenless HTTP: %d", response.StatusCode)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("HTTP did not start after IPC failure: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			request, _ := http.NewRequest(http.MethodGet, url, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			want := http.StatusUnauthorized
			if token != "" {
				want = http.StatusOK
			}
			if response.StatusCode != want {
				t.Fatalf("bootstrap HTTP: %d, want %d", response.StatusCode, want)
			}
			entries := logs.General.Snapshot()
			found := false
			for _, entry := range entries {
				found = found || strings.Contains(entry.Content, "local management unavailable")
			}
			if !found {
				t.Fatal("IPC unavailable was not logged")
			}
			for _, name := range []string{"frostagent.db", "control-plane-logs"} {
				if _, err := os.Stat(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(unrelated, "data", name)); !os.IsNotExist(err) {
					t.Fatal("persistence ignored the application's data directory")
				}
			}
		})
	}
}
