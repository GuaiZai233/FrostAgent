package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/instance"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestDockerStartupRejectsUnsafeConfiguration(t *testing.T) {
	previousContainer, previousLoopback := instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly
	previousListen, previousWSListen := instanceconfig.DefaultListenAddr, instanceconfig.DefaultWSListenAddr
	instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = "true", "false"
	t.Cleanup(func() {
		instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = previousContainer, previousLoopback
		instanceconfig.DefaultListenAddr, instanceconfig.DefaultWSListenAddr = previousListen, previousWSListen
	})
	for _, tc := range []struct {
		name, token, management, adapter, wantError string
	}{
		{
			name: "missing credential", management: "127.0.0.1:0", adapter: "127.0.0.1:1",
			wantError: dockerBootstrapTokenKey,
		},
		{
			name: "shared listener port", token: "synthetic-startup-control-token-with-32-bytes",
			management: "127.0.0.1:0", adapter: "0.0.0.0:0",
			wantError: "management and adapter listeners must use different ports",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv(dockerBootstrapTokenKey, tc.token)
			instanceconfig.DefaultListenAddr, instanceconfig.DefaultWSListenAddr = tc.management, tc.adapter
			if err := run(); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("startup error=%v, want error containing %q", err, tc.wantError)
			}
		})
	}
}

func TestDockerControlTokenBootstrap(t *testing.T) {
	previousContainer, previousLoopback := instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly
	t.Cleanup(func() {
		instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = previousContainer, previousLoopback
	})
	const token = "synthetic-bootstrap-token-with-at-least-32-bytes"
	for _, tc := range []struct {
		name, bootstrap, existing, admin string
		container, loopback, wantError   bool
	}{
		{name: "first startup", container: true, bootstrap: token},
		{name: "missing credential", container: true, wantError: true},
		{name: "short credential", container: true, bootstrap: "short", wantError: true},
		{name: "multiline credential", container: true, bootstrap: token + "\ninvalid", wantError: true},
		{name: "NUL credential", container: true, bootstrap: token + "\x00invalid", wantError: true},
		{name: "oversized credential", container: true, bootstrap: strings.Repeat("x", 4097), wantError: true},
		{name: "non ASCII credential", container: true, bootstrap: strings.Repeat("狐", 12), wantError: true},
		{name: "control character", container: true, bootstrap: token + "\tinvalid", wantError: true},
		{name: "internal space", container: true, bootstrap: token + " invalid", wantError: true},
		{name: "existing token wins", container: true, existing: "synthetic-existing-token", bootstrap: token},
		{name: "existing admin wins", container: true, admin: "synthetic-admin-token", bootstrap: token},
		{name: "existing token needs no bootstrap", container: true, existing: "synthetic-existing-token"},
		{name: "native startup"},
		{name: "host namespace startup", container: true, loopback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = "false", "false"
			if tc.container {
				instanceconfig.ContainerManagement = "true"
			}
			if tc.loopback {
				instanceconfig.ManagementLoopbackOnly = "true"
			}
			root := t.TempDir()
			gateway, err := newStorageGateway(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			manager, _, _ := gateway.snapshot()
			global := manager.GlobalConfig()
			for key, value := range map[string]string{"MCP_CONTROL_TOKEN": tc.existing, "ADMIN_TOKEN": tc.admin} {
				if value != "" {
					if err := global.Update(key, value, false); err != nil {
						t.Fatal(err)
					}
				}
			}
			err = initializeDockerControlToken(global, func(string) string { return tc.bootstrap })
			if (err != nil) != tc.wantError {
				t.Fatalf("bootstrap error=%v, wantError=%t", err, tc.wantError)
			}
			if err != nil && strings.Contains(err.Error(), token) {
				t.Fatal("bootstrap error disclosed credential")
			}
			want := tc.existing
			if !tc.wantError && tc.container && !tc.loopback && tc.existing == "" && tc.admin == "" {
				want = tc.bootstrap
			}
			if global.Get("MCP_CONTROL_TOKEN") != want {
				t.Fatal("saved token changed unexpectedly")
			}
			gateway.Close()
			gateway, err = newStorageGateway(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			manager, _, _ = gateway.snapshot()
			if manager.GlobalConfig().Get("MCP_CONTROL_TOKEN") != want || manager.GlobalConfig().Get("ADMIN_TOKEN") != tc.admin {
				t.Fatal("bootstrap did not preserve SQL credentials across restart")
			}
		})
	}
}

func TestDockerControlTokenDatabaseSwitch(t *testing.T) {
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("PostgreSQL test DSN must be a postgres URI")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("fa_docker_bootstrap_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("remove Docker bootstrap test schema: %v", err)
		}
		admin.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	target := storage.Config{Backend: storage.Postgres, DSN: parsed.String()}
	previousContainer, previousLoopback := instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly
	instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = "true", "false"
	t.Cleanup(func() {
		instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = previousContainer, previousLoopback
	})
	gateway, err := newStorageGateway(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	const original = "synthetic-original-control-token-with-32-bytes"
	const bootstrap = "synthetic-target-control-token-with-32-bytes"
	manager, _, _ := gateway.snapshot()
	if err := manager.GlobalConfig().Update("MCP_CONTROL_TOKEN", original, false); err != nil {
		t.Fatal(err)
	}
	input := ""
	gateway.prepare = func(next *instance.Manager) error {
		return initializeDockerControlToken(next.GlobalConfig(), func(string) string { return input })
	}
	gateway.apply = func(next *instance.Manager) error {
		if next.GlobalConfig().Get("MCP_CONTROL_TOKEN") == "" {
			t.Error("listeners were applied before target credential initialization")
		}
		return next.ApplyGlobalSettings()
	}
	assertAccess := func(token string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
		request.RemoteAddr, request.Host = "172.20.0.1:12345", "localhost:8080"
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("published API status=%d, want=%d", response.Code, want)
		}
	}
	if err := gateway.switchTo(context.Background(), target); err == nil {
		t.Fatal("unconfigured target database was accepted without bootstrap input")
	}
	manager, config, _ := gateway.snapshot()
	stored, err := gateway.bootstrap.Load(context.Background())
	if err != nil || manager == nil || config.Backend != storage.SQLite || stored.Backend != storage.SQLite {
		t.Fatal("rejected target did not recover original database and selection")
	}
	assertAccess(original, http.StatusOK)
	assertAccess(bootstrap, http.StatusUnauthorized)
	input = bootstrap
	if err := gateway.switchTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	assertAccess(bootstrap, http.StatusOK)
	assertAccess(original, http.StatusUnauthorized)
	if err := gateway.switchTo(context.Background(), storage.Config{Backend: storage.SQLite}); err != nil {
		t.Fatal(err)
	}
	assertAccess(original, http.StatusOK)
	input = ""
	if err := gateway.switchTo(context.Background(), target); err != nil {
		t.Fatal("saved target credential required bootstrap input again:", err)
	}
	assertAccess(bootstrap, http.StatusOK)
}
