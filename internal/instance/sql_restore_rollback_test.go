package instance

import (
	"FrostAgent/internal/backup"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/storage"
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func assertRestoreFailureLeavesCatalog(t *testing.T, m *Manager) {
	t.Helper()
	source, err := m.Create("Rollback Source")
	if err != nil || source.Error != "" {
		t.Fatalf("create source: %+v, %v", source, err)
	}
	catalog := modelrouter.NewSQL(m.db, "")
	shared := catalog.Draft()
	shared.Endpoints = []modelrouter.Endpoint{{ID: "existing-provider", DisplayName: "Existing Provider", BaseURL: "https://existing.invalid/v1", Enabled: true}}
	shared.Models = []modelrouter.Model{{ID: "existing-model", DisplayName: "Existing Model", EndpointID: "existing-provider", UpstreamModel: "synthetic-model", Enabled: true}}
	if err := catalog.SaveDraft(shared); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SetDraftEndpointSecret("existing-provider", "synthetic-existing-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	routes := modelrouter.NewSQL(m.db, source.ID)
	bound := routes.Draft()
	bound.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "existing-model"}
	if err := routes.SaveDraft(bound); err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Publish(); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var tampered bytes.Buffer
	writer := zip.NewWriter(&tampered)
	for _, member := range reader.File {
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		switch member.Name {
		case "setting.json":
			var settings backup.Settings
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			settings.ModelRouter.Endpoints[0].ID = "imported-provider"
			settings.ModelRouter.Endpoints[0].DisplayName = "Imported Provider"
			settings.ModelRouter.Endpoints[0].BaseURL = "https://imported.invalid/v1"
			settings.ModelRouter.Models[0].ID = "imported-model"
			settings.ModelRouter.Models[0].DisplayName = "Imported Model"
			settings.ModelRouter.Models[0].EndpointID = "imported-provider"
			binding := settings.ModelRouter.GlobalBindings[modelrouter.WorkloadDialogue]
			binding.ModelID = "imported-model"
			settings.ModelRouter.GlobalBindings[modelrouter.WorkloadDialogue] = binding
			data, err = json.Marshal(settings)
		case "memory.json":
			var memories backup.Memories
			if err := json.Unmarshal(data, &memories); err != nil {
				t.Fatal(err)
			}
			memories.PrivateEntries = append(memories.PrivateEntries, memories.PrivateEntries...)
			if len(memories.PrivateEntries) == 0 {
				data = []byte(`{"format_version":1,"private_entries":[{"id":"duplicate-memory"},{"id":"duplicate-memory"}]}`)
			} else {
				memories.PrivateEntries = append(memories.PrivateEntries, memories.PrivateEntries[0])
				data, err = json.Marshal(memories)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		out, err := writer.Create(member.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	before := catalog.Active()
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := m.RestoreInstanceZIP("Failed Restore", tampered.Bytes()); err == nil || !strings.Contains(err.Error(), "duplicate memory ID") {
			t.Fatalf("attempt %d did not fail after settings import: %v", attempt, err)
		}
		list, _ := m.List()
		if len(list) != 1 || list[0].ID != source.ID {
			t.Fatalf("attempt %d left a restored instance: %+v", attempt, list)
		}
		current := catalog.Active()
		if current.Revision != before.Revision || len(current.Endpoints) != 1 || len(current.Models) != 1 ||
			current.Endpoints[0].ID != "existing-provider" || current.Models[0].ID != "existing-model" {
			t.Fatalf("attempt %d changed shared catalog: %+v", attempt, current)
		}
		var key string
		if err := m.db.SQL.QueryRow(m.db.Bind(`SELECT secret FROM provider_secrets WHERE ref = ?`), "endpoint/existing-provider").Scan(&key); err != nil || key != "synthetic-existing-key" {
			t.Fatalf("attempt %d changed existing credential: %v", attempt, err)
		}
		resolved, routeErr := routes.Resolve(modelrouter.WorkloadDialogue, modelrouter.Scope{})
		if routeErr != nil || resolved.EndpointID != "existing-provider" {
			t.Fatalf("attempt %d changed other instance route: %+v, %v", attempt, resolved, routeErr)
		}
	}
}

func TestSQLiteRestoreFailureRollsBackSharedCatalog(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	assertRestoreFailureLeavesCatalog(t, m)
}

func TestPostgresRestoreFailureRollsBackSharedCatalog(t *testing.T) {
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("fa_restore_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	root := t.TempDir()
	db, err := storage.OpenWithConfig(context.Background(), root, storage.Config{Backend: storage.Postgres, DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewDatabaseWithDB(root, db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	defer m.Close()
	assertRestoreFailureLeavesCatalog(t, m)
}
