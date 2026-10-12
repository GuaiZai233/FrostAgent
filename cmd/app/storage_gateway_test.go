package main

import (
	"FrostAgent/internal/instance"
	"FrostAgent/internal/storage"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func hasInstance(instances []instance.Info, id string) bool {
	for _, item := range instances {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestStorageGatewayFailedSwitchKeepsSQLiteData(t *testing.T) {
	root := t.TempDir()
	gateway, err := newStorageGateway(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { gateway.Close() }()
	manager, _, _ := gateway.snapshot()
	created, err := manager.Create("SQLite Instance")
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.switchTo(context.Background(), storage.Config{Backend: storage.Postgres}); err == nil {
		t.Fatal("empty PostgreSQL address switched the database")
	}
	if err := gateway.switchTo(context.Background(), storage.Config{
		Backend: storage.Postgres,
		DSN:     "postgres://synthetic:synthetic@127.0.0.1:1/synthetic?connect_timeout=1",
	}); err == nil {
		t.Fatal("unavailable PostgreSQL database switched successfully")
	}
	manager, config, _ := gateway.snapshot()
	if manager == nil || config.Backend != storage.SQLite {
		t.Fatalf("SQLite was not restored after failed switch: %#v", config)
	}
	instances, _ := manager.List()
	if len(instances) != 1 || instances[0].ID != created.ID {
		t.Fatalf("SQLite data was lost after failed switch: %#v", instances)
	}
	stored, err := gateway.bootstrap.Load(context.Background())
	if err != nil || stored.Backend != storage.SQLite {
		t.Fatalf("failed switch changed bootstrap selection: %#v, %v", stored, err)
	}

	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/storage", nil))
	var view storage.Config
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || view.Backend != storage.SQLite {
		t.Fatalf("storage API state is wrong: %#v, %v", view, err)
	}
	response = httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/storage", strings.NewReader(`{"backend":"postgres","dsn":""}`)))
	if response.Code == http.StatusOK {
		t.Fatal("storage API accepted an empty PostgreSQL address")
	}
}

func TestPostgresGatewaySwitchLoadsEachDatabaseWithoutMigration(t *testing.T) {
	dsn := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	root := t.TempDir()
	gateway, err := newStorageGateway(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { gateway.Close() }()
	sqlite, _, _ := gateway.snapshot()
	sqliteInstance, err := sqlite.Create("SQLite Only")
	if err != nil {
		t.Fatal(err)
	}
	postgresConfig := storage.Config{Backend: storage.Postgres, DSN: dsn}
	if err := gateway.switchTo(context.Background(), postgresConfig); err != nil {
		t.Fatal(err)
	}
	postgres, _, _ := gateway.snapshot()
	initial, _ := postgres.List()
	if hasInstance(initial, sqliteInstance.ID) {
		t.Fatalf("SQLite data migrated without request: %#v", initial)
	}
	postgresInstance, err := postgres.Create(fmt.Sprintf("Postgres-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.GlobalConfig().Update("LISTEN_ADDR", "127.0.0.1:19080", false); err != nil {
		t.Fatal(err)
	}
	if err := gateway.switchTo(context.Background(), storage.Config{Backend: storage.SQLite}); err != nil {
		t.Fatal(err)
	}
	sqlite, _, _ = gateway.snapshot()
	sqliteRows, _ := sqlite.List()
	if !hasInstance(sqliteRows, sqliteInstance.ID) || hasInstance(sqliteRows, postgresInstance.ID) {
		t.Fatalf("switch back did not recover SQLite data: %#v", sqliteRows)
	}
	postBody, err := json.Marshal(postgresConfig)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/storage", strings.NewReader(string(postBody))))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"listen_addr":"127.0.0.1:19080"`) {
		t.Fatalf("switch response lost the new management address: %d %s", response.Code, response.Body.String())
	}
	postgres, _, _ = gateway.snapshot()
	postgresRows, _ := postgres.List()
	if !hasInstance(postgresRows, postgresInstance.ID) || hasInstance(postgresRows, sqliteInstance.ID) {
		t.Fatalf("switch back did not recover PostgreSQL data: %#v", postgresRows)
	}
	gateway.Close()
	gateway, err = newStorageGateway(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	postgres, config, _ := gateway.snapshot()
	postgresRows, _ = postgres.List()
	if config.Backend != storage.Postgres || !hasInstance(postgresRows, postgresInstance.ID) {
		t.Fatalf("restart did not load selected database: %#v, %#v", config, postgresRows)
	}
}
