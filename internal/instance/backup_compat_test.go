package instance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

// rewriteBackupMember returns archive with one JSON member edited in place.
func rewriteBackupMember(t *testing.T, archive []byte, member string, edit func(map[string]any)) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	found := false
	for _, f := range reader.File {
		source, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(source)
		source.Close()
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == member {
			found = true
			var value map[string]any
			if err := json.Unmarshal(content, &value); err != nil {
				t.Fatal(err)
			}
			edit(value)
			if content, err = json.Marshal(value); err != nil {
				t.Fatal(err)
			}
		}
		target, err := writer.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("backup has no %s member", member)
	}
	return buffer.Bytes()
}

func newBackupTestManager(t *testing.T) (*Manager, []byte) {
	t.Helper()
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	source, err := m.Create("Synthetic compat source")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m, archive
}

func TestVerifyRejectsMCPURLThatRestoreRejects(t *testing.T) {
	m, archive := newBackupTestManager(t)
	broken := rewriteBackupMember(t, archive, "setting.json", func(settings map[string]any) {
		cfg, _ := settings["mcp"].(map[string]any)
		if cfg == nil {
			cfg = map[string]any{}
			settings["mcp"] = cfg
		}
		servers, _ := cfg["servers"].([]any)
		cfg["servers"] = append(servers, map[string]any{"id": "review", "name": "review", "enabled": false,
			"transport": map[string]any{"type": "sse", "url": "https://"}})
	})
	_, verifyErr := m.VerifyBackup(context.Background(), broken, "Synthetic compat restore")
	before, _ := m.List()
	_, restoreErr := m.RestoreInstanceZIP("Synthetic compat restore", broken)
	after, _ := m.List()
	if restoreErr == nil {
		t.Fatal("restore accepted an MCP server without a usable HTTP URL")
	}
	if verifyErr == nil {
		t.Fatalf("verification accepted a backup that restore rejects: %v", restoreErr)
	}
	if !strings.Contains(verifyErr.Error(), "review") || len(before) != len(after) {
		t.Fatalf("verify error=%v, instances before=%d after=%d", verifyErr, len(before), len(after))
	}
}

func TestRestoreCompatibilityUsesMinimumVersion(t *testing.T) {
	m, archive := newBackupTestManager(t)
	newer := rewriteBackupMember(t, archive, "manifest.json", func(manifest map[string]any) {
		manifest["database_schema_version"] = storage.SupportedSchemaVersion + 1
		manifest["database_minimum_compatible_version"] = storage.SupportedSchemaVersion
	})
	if plan, err := m.VerifyBackup(context.Background(), newer, "Synthetic newer source"); err != nil || plan["valid"] != true {
		t.Fatalf("newer source with compatible content rejected: %v %v", plan, err)
	}
	restored, err := m.RestoreInstanceZIP("Synthetic newer source", newer)
	if err != nil {
		t.Fatal("restore rejected a compatible newer-source backup:", err)
	}
	if restored.Enabled {
		t.Fatal("restored instance must start disabled")
	}

	requiresNewer := rewriteBackupMember(t, archive, "manifest.json", func(manifest map[string]any) {
		manifest["database_schema_version"] = storage.SupportedSchemaVersion + 1
		manifest["database_minimum_compatible_version"] = storage.SupportedSchemaVersion + 1
	})
	if _, err := m.VerifyBackup(context.Background(), requiresNewer, "Synthetic future"); err == nil {
		t.Fatal("verification accepted a backup that needs newer database capabilities")
	}
	if _, err := m.RestoreInstanceZIP("Synthetic future", requiresNewer); err == nil {
		t.Fatal("restore accepted a backup that needs newer database capabilities")
	}
}
