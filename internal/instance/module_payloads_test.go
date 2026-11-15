package instance

import (
	"bytes"
	"context"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestFutureModuleInstanceLifecycle(t *testing.T) {
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Synthetic future modules")
	if err != nil || source.Error != "" {
		t.Fatalf("create: %+v %v", source, err)
	}
	p := storage.ModulePayload{ModuleID: "future.plugin", RecordID: "one", FormatVersion: 20, Data: []byte{0, 255, 128, 'a'}}
	p.SHA256 = storage.ModulePayloadDigest(p.Data)
	if err := m.db.SaveModulePayload(context.Background(), source.ID, p, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.SQL.Exec(m.db.Bind(`UPDATE schema_meta SET version=?,min_compatible_version=18`), storage.SchemaVersion+3); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := m.RestoreInstanceZIP("Synthetic restored modules", archive)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Enabled {
		t.Fatal("restored instance enabled")
	}
	archive, err = m.InstanceZIP(restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := backup.DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Modules) != 1 || !bytes.Equal(decoded.Modules[0].Data, p.Data) || !decoded.Modules[0].RequiresRebind {
		t.Fatal("unknown module changed")
	}
	if err := m.PrepareDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
	records, err := m.db.ModulePayloads(context.Background(), restored.ID)
	if err != nil || len(records) != 0 {
		t.Fatal("orphan module rows")
	}
	records, err = m.db.ModulePayloads(context.Background(), source.ID)
	if err != nil || len(records) != 1 {
		t.Fatal("source module affected by deletion")
	}
}
