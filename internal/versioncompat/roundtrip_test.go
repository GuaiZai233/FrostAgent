// Package versioncompat exercises two independently built releases against the
// same database and their public backup/restore paths. It contains test code only.
package versioncompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/instance"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/tokenusage"
)

var formats = []string{"", "openai_chat_completions", "openai_responses", "anthropic_messages", "gemini_generate_content", "custom"}

const sourceID = "synthetic-roundtrip"

func checked(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestReleaseProbe is invoked by scripts/test_schema_roundtrip.py in separate
// release binaries. Ordinary go test does not access persistent user data.
func TestReleaseProbe(t *testing.T) {
	op := os.Getenv("FROSTAGENT_COMPAT_PROBE")
	if op == "" {
		t.Skip("cross-release driver supplies isolated fixture paths")
	}
	if (op == "edit-old" || op == "seed-legacy") && storage.SchemaVersion != 15 {
		t.Fatal("old-version probe must be built from the v15 compatibility branch")
	}
	if (op == "seed" || op == "upgrade") && storage.SchemaVersion != 16 {
		t.Fatal("new-version probe must be built from v16")
	}
	root := os.Getenv("FROSTAGENT_COMPAT_ROOT")
	if root == "" {
		t.Fatal("missing isolated fixture root")
	}
	ctx := context.Background()
	db, err := storage.Open(ctx, root)
	checked(t, err)
	defer db.Close()
	id := sourceID
	if value := os.Getenv("FROSTAGENT_COMPAT_INSTANCE"); value != "" {
		id = value
	}
	catalog := modelrouter.NewSQL(db, "")
	checked(t, catalog.LoadError())
	switch op {
	case "init":
	case "seed", "seed-legacy":
		checked(t, db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: "Synthetic roundtrip", CreatedAt: time.Now().UTC()}, 2))
		cfg := catalog.Draft()
		for i, format := range formats {
			if op == "seed-legacy" {
				format = ""
			}
			base := fmt.Sprintf("https://relay.invalid/proxy-%d", i)
			if format == "custom" {
				base += "/exact?route=fixture"
			}
			endpointID, modelID := fmt.Sprintf("synthetic-endpoint-%d", i), fmt.Sprintf("synthetic-model-%d", i)
			cfg.Endpoints = append(cfg.Endpoints, modelrouter.Endpoint{ID: endpointID, DisplayName: fmt.Sprintf("Synthetic Endpoint %d", i), BaseURL: base, APIFormat: format, Enabled: true})
			cfg.Models = append(cfg.Models, modelrouter.Model{ID: modelID, DisplayName: fmt.Sprintf("Synthetic Model %d", i), EndpointID: endpointID, UpstreamModel: "synthetic-upstream", Enabled: true})
		}
		checked(t, catalog.SaveDraft(cfg))
		_, err = catalog.SetDraftEndpointSecret("synthetic-endpoint-0", "synthetic-original-key")
		checked(t, err)
		_, err = catalog.Publish()
		checked(t, err)
		routes := modelrouter.NewSQL(db, id)
		cfg = routes.Draft()
		for i, workload := range []modelrouter.Workload{modelrouter.WorkloadDialogue, modelrouter.WorkloadSubagent, modelrouter.WorkloadVision, modelrouter.WorkloadReflection, modelrouter.WorkloadMemoryExtract, modelrouter.WorkloadGroupCompact} {
			cfg.GlobalBindings[workload] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: fmt.Sprintf("synthetic-model-%d", i)}
		}
		checked(t, routes.SaveDraft(cfg))
		_, err = routes.Publish()
		checked(t, err)
		config := storage.DefaultComputerUseConfig()
		config.BridgeURL = "https://bridge.invalid"
		config.Token = "synthetic-private-bridge-key"
		checked(t, db.SaveComputerUseConfig(ctx, id, config))
		checked(t, db.SaveComputerUseProject(ctx, id, storage.ComputerUseProject{ID: "synthetic-project", Path: "synthetic-repository", Baseline: "main", Shell: "synthetic-shell"}))
		stamp := "2026-01-01T00:00:00Z"
		checked(t, db.SaveComputerUseTask(ctx, id, storage.ComputerUseTask{ID: "synthetic-task", ProjectID: "synthetic-project", ActorID: "synthetic-actor", ActorKind: "qq_private", State: "paused", Generation: 7, CreatedAt: stamp, UpdatedAt: stamp}))
		checked(t, memory.NewSQLStore(db, id).Save(memory.MemoryEntry{ID: "synthetic-memory", Owner: "synthetic-owner", Content: "preserved memory", Source: memory.SourceManual, CreatedAt: time.Now(), UpdatedAt: time.Now()}))
		usage, _ := tokenusage.New(db, id, "")
		checked(t, usage.Restore(ctx, tokenusage.Snapshot{Version: 1, Modules: []tokenusage.Module{{Module: "dialogue", Requests: 2, Input: tokenusage.Counter{Value: 23}, Output: tokenusage.Counter{Value: 9}, Total: tokenusage.Counter{Value: 32}}}}))
	case "upgrade":
		cfg := catalog.Draft()
		for i := range cfg.Endpoints {
			cfg.Endpoints[i].APIFormat = formats[i]
			if formats[i] == "custom" {
				cfg.Endpoints[i].BaseURL += "/exact?route=fixture"
			}
		}
		checked(t, catalog.SaveDraft(cfg))
		_, err = catalog.Publish()
		checked(t, err)
	case "edit-old":
		cfg := catalog.Draft()
		// Build the payload the old UI/RPC actually supplies: no api_format field.
		for i, e := range cfg.Endpoints {
			cfg.Endpoints[i] = modelrouter.Endpoint{ID: e.ID, DisplayName: e.DisplayName, BaseURL: e.BaseURL, APIKeySource: e.APIKeySource, APIKeyRef: e.APIKeyRef, Enabled: e.Enabled}
		}
		cfg.Endpoints[0].DisplayName = "Edited in v15"
		checked(t, catalog.SaveDraft(cfg))
		_, err = catalog.SetDraftEndpointSecret("synthetic-endpoint-0", "synthetic-rotated-key")
		checked(t, err)
		_, err = catalog.Publish()
		checked(t, err)
		_, err = db.SQL.Exec(`INSERT INTO settings(scope,instance_id,key,value) VALUES ('instance',?,'BOT_NAME','Edited in v15') ON CONFLICT(scope,instance_id,key) DO UPDATE SET value=excluded.value`, id)
		checked(t, err)
	case "verify", "verify-restored":
		cfg, err := catalog.ActiveChecked()
		checked(t, err)
		if len(cfg.Endpoints) != len(formats) {
			t.Fatalf("endpoint count=%d", len(cfg.Endpoints))
		}
		for i, e := range cfg.Endpoints {
			if e.APIFormat != formats[i] {
				t.Fatalf("endpoint %d lost format: %q", i, e.APIFormat)
			}
			if i == 5 && !strings.HasSuffix(e.BaseURL, "/exact?route=fixture") {
				t.Fatal("custom URL changed")
			}
		}
		var name, content string
		checked(t, db.SQL.QueryRow(`SELECT value FROM settings WHERE scope='instance' AND instance_id=? AND key='BOT_NAME'`, id).Scan(&name))
		if name != "Edited in v15" {
			t.Fatal("old-version edit lost")
		}
		checked(t, db.SQL.QueryRow(`SELECT content FROM memory_entries WHERE instance_id=? AND id='synthetic-memory'`, id).Scan(&content))
		if content != "preserved memory" {
			t.Fatal("memory lost")
		}
		tasks, err := db.ComputerUseTasks(ctx, id)
		checked(t, err)
		if len(tasks) != 1 || tasks[0].Generation != 7 {
			t.Fatal("Computer Use metadata lost")
		}
		config, err := db.ComputerUseConfig(ctx, id)
		checked(t, err)
		var key string
		err = db.SQL.QueryRow(`SELECT secret FROM provider_secrets WHERE ref=?`, cfg.Endpoints[0].APIKeyRef).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		checked(t, err)
		if op == "verify" {
			if key != "synthetic-rotated-key" || config.Token != "synthetic-private-bridge-key" {
				t.Fatal("in-place credentials changed")
			}
		} else if key != "" || config.Token != "" || tasks[0].Code != "restored_requires_rebind" {
			t.Fatal("backup credential exclusion/task recovery policy changed")
		}
		usage, _ := tokenusage.New(db, id, "")
		snapshot, err := usage.Snapshot(ctx)
		checked(t, err)
		var total int64
		for _, module := range snapshot.Modules {
			if module.Module == "dialogue" {
				total = module.Total.Value
			}
		}
		if total != 32 {
			t.Fatal("usage lost")
		}
		var version int
		checked(t, db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&version))
		if version != 16 {
			t.Fatalf("database was downgraded: %d", version)
		}
	case "backup":
		archive, err := backup.BuildInstanceZIP(db, id, filepath.Join(root, "unused-sticker-files"))
		checked(t, err)
		checked(t, os.WriteFile(os.Getenv("FROSTAGENT_COMPAT_ARCHIVE"), archive, 0600))
	case "restore":
		archive, err := os.ReadFile(os.Getenv("FROSTAGENT_COMPAT_ARCHIVE"))
		checked(t, err)
		// Use the full public restore path, including catalog/instance rollback.
		manager, err := instance.NewDatabaseWithDB(root, db)
		checked(t, err)
		defer manager.Close()
		restored, err := manager.RestoreInstanceZIP("Restored roundtrip", archive)
		checked(t, err)
		raw, _ := json.Marshal(restored.ID)
		checked(t, os.WriteFile(os.Getenv("FROSTAGENT_COMPAT_RESULT"), raw, 0600))
	default:
		t.Fatalf("unknown probe operation %q", op)
	}
}
