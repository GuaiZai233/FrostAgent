package instance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/tokenusage"
)

func TestTokenUsageZIPRestoreAndLegacyCompatibility(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Usage Source")
	if err != nil {
		t.Fatal(err)
	}
	usage, _ := tokenusage.New(m.db, source.ID, "")
	if err := usage.Record("sticker_summary", &core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}); err != nil {
		t.Fatal(err)
	}
	// A failed final response must remain visible in the backup's fresh Store.
	if _, err := m.db.SQL.Exec(`CREATE TRIGGER reject_usage BEFORE INSERT ON settings WHEN NEW.key='dialogue' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := usage.Record("dialogue", nil); err == nil {
		t.Fatal("expected accounting failure")
	}
	if _, err := m.db.SQL.Exec(`DROP TRIGGER reject_usage`); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := backup.DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.TokenUsage == nil || !decoded.TokenUsage.Incomplete {
		t.Fatal("ZIP omitted usage or failure marker")
	}
	restored, err := m.RestoreInstanceZIP("Usage Restored", archive)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := tokenusage.New(m.db, restored.ID, "")
	snap, err := target.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range snap.Modules {
		if row.Module == "sticker_summary" {
			found = row.Requests == 1 && row.Total.Value == 15
		}
	}
	if !found || !snap.Incomplete {
		t.Fatalf("restore lost usage: %+v", snap)
	}
	rewrite := func(replacement []byte) []byte {
		t.Helper()
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		writer := zip.NewWriter(&output)
		for _, member := range reader.File {
			if member.Name == "token_usage.json" && replacement == nil {
				continue
			}
			stream, err := member.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(stream)
			stream.Close()
			if err != nil {
				t.Fatal(err)
			}
			if member.Name == "token_usage.json" {
				data = replacement
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
		return output.Bytes()
	}
	legacy, err := m.RestoreInstanceZIP("Legacy Usage", rewrite(nil))
	if err != nil {
		t.Fatal(err)
	}
	legacyStore, _ := tokenusage.New(m.db, legacy.ID, "")
	legacySnap, err := legacyStore.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range legacySnap.Modules {
		if row.Requests != 0 {
			t.Fatal("legacy backup invented usage")
		}
	}
	if _, err := m.RestoreInstanceZIP("Invalid Usage", rewrite([]byte(`{"version":99}`))); err == nil {
		t.Fatal("invalid usage backup accepted")
	}
	if instances, _ := m.List(); len(instances) != 3 {
		t.Fatal("invalid backup created an instance")
	}
}

func TestCorruptUsageDoesNotBlockLegacyRuntime(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "Corrupt Usage")
	path := filepath.Join(m.dir(info.ID), "token_usage.json")
	corrupt := []byte(`{"version":99}`)
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	runtime := m.instances[info.ID].runtime
	if runtime == nil || runtime.Engine == nil || runtime.Engine.TokenUsage != nil || runtime.Scope.Context().Err() != nil {
		t.Fatal("usage corruption blocked runtime")
	}
	response := rpc(t, m, info.ID, "BotStatusService/GetOverview", "{}", false)
	if response.Code != 200 {
		t.Fatalf("overview failed: %d %s", response.Code, response.Body.String())
	}
	var overview map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview["tokenUsage"] != nil {
		t.Fatal("unavailable usage reported as available")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, corrupt) {
		t.Fatal("corrupt accounting file overwritten")
	}
}
