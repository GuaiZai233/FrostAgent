package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestOpaqueModuleBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"synthetic-source", "synthetic-target"} {
		if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 2); err != nil {
			t.Fatal(err)
		}
	}
	p := storage.ModulePayload{ModuleID: "future.plugin", RecordID: "opaque", FormatVersion: 37, Data: []byte{255, 0, 1, '{', '}', 128}}
	p.SHA256 = storage.ModulePayloadDigest(p.Data)
	if err := db.SaveModulePayload(ctx, "synthetic-source", p, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(db.Bind(`UPDATE schema_meta SET version=?,min_compatible_version=18`), storage.SchemaVersion+5); err != nil {
		t.Fatal(err)
	}
	archive, err := BuildInstanceZIP(db, "synthetic-source", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Modules) != 1 || !bytes.Equal(decoded.Modules[0].Data, p.Data) {
		t.Fatal("unknown bytes changed")
	}
	if err := db.RestoreModulePayloads(ctx, "synthetic-target", decoded.Modules); err != nil {
		t.Fatal(err)
	}
	archive, err = BuildInstanceZIP(db, "synthetic-target", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Modules) != 1 || !bytes.Equal(decoded.Modules[0].Data, p.Data) || !decoded.Modules[0].RequiresRebind {
		t.Fatal("restore/re-export changed payload or enabled execution")
	}
	for name, records := range map[string][]storage.ModulePayload{"duplicate": {p, p}, "checksum": {p}} {
		t.Run(name, func(t *testing.T) {
			if name == "checksum" {
				records[0].SHA256 = "bad"
			}
			bad, err := json.Marshal(ModulePayloads{ProtocolVersion: 1, Records: records})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeInstanceZIP(replaceModuleMember(t, archive, bad)); err == nil {
				t.Fatal("invalid module archive accepted")
			}
		})
	}
}

func replaceModuleMember(t *testing.T, archive, data []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range reader.File {
		if file.Name == "modules.json" {
			member, err := writer.Create(file.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = member.Write(data); err != nil {
				t.Fatal(err)
			}
		} else if err := writer.Copy(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
