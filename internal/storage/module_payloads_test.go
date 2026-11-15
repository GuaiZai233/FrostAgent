package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestModulePayloadRoundTripAndWriteProtection(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			ctx := context.Background()
			for _, id := range []string{"synthetic-a", "synthetic-b", "synthetic-restored"} {
				compatibilityExec(t, db, `INSERT INTO instances(id,name,created_at) VALUES (?,?, 'now')`, id, id)
			}
			data := []byte{0, 255, 128, '{', 'x', '}', 0}
			p := ModulePayload{ModuleID: "future.module", RecordID: "record/one", FormatVersion: 2, Data: data, SHA256: ModulePayloadDigest(data)}
			for _, id := range []string{"synthetic-a", "synthetic-b"} {
				if err := db.SaveModulePayload(ctx, id, p, nil); err != nil {
					t.Fatal(err)
				}
			}
			stale := p
			stale.FormatVersion = 1
			old := stale.Revision()
			if err := db.SaveModulePayload(ctx, "synthetic-a", stale, &old); !errors.Is(err, ErrModulePayloadConflict) {
				t.Fatalf("old writer replaced newer format: %v", err)
			}
			future := p.Revision()
			if err := db.SaveModulePayload(ctx, "synthetic-a", stale, &future); !errors.Is(err, ErrModulePayloadConflict) {
				t.Fatalf("format downgrade accepted: %v", err)
			}
			if err := db.DeleteModulePayload(ctx, "synthetic-a", p.ModuleID, p.RecordID, old); !errors.Is(err, ErrModulePayloadConflict) {
				t.Fatalf("old writer deleted newer format: %v", err)
			}
			if err := db.RestoreModulePayloads(ctx, "synthetic-restored", []ModulePayload{p}); err != nil {
				t.Fatal(err)
			}
			if err := db.SaveModulePayload(ctx, "synthetic-restored", p, &future); err != nil {
				t.Fatal(err)
			}
			compatibilityExec(t, db, `UPDATE schema_meta SET version=?,min_compatible_version=18`, SchemaVersion+1)
			compatibilityClose(t, db)
			db, err := OpenWithConfig(ctx, root, config)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, id := range []string{"synthetic-a", "synthetic-b", "synthetic-restored"} {
				records, err := db.ModulePayloads(ctx, id)
				if err != nil || len(records) != 1 || !bytes.Equal(records[0].Data, data) || records[0].RequiresRebind != (id == "synthetic-restored") {
					t.Fatalf("payload changed after restart: %+v %v", records, err)
				}
			}
			if err := db.RequireNativeSchema(ctx, "export"); err != nil {
				t.Fatalf("valid future module blocked: %v", err)
			}
			if err := db.PrepareDeletion(ctx, "synthetic-a", 1); err != nil {
				t.Fatal(err)
			}
			if err := db.ConfirmDeletion(ctx, "synthetic-a"); err != nil {
				t.Fatal(err)
			}
			rows, err := db.ModulePayloads(ctx, "synthetic-a")
			if err != nil || len(rows) != 0 {
				t.Fatalf("orphan payload after deletion: %v", err)
			}
			rows, err = db.ModulePayloads(ctx, "synthetic-b")
			if err != nil || len(rows) != 1 {
				t.Fatalf("cross-instance deletion: %v", err)
			}
			if err := db.RebindModulePayload(ctx, "synthetic-restored", p.ModuleID, p.RecordID, old); !errors.Is(err, ErrModulePayloadConflict) {
				t.Fatalf("unknown format rebound: %v", err)
			}
			if err := db.RebindModulePayload(ctx, "synthetic-restored", p.ModuleID, p.RecordID, future); err != nil {
				t.Fatal(err)
			}
			rows, err = db.ModulePayloads(ctx, "synthetic-restored")
			if err != nil || rows[0].RequiresRebind {
				t.Fatalf("explicit binding did not persist: %v", err)
			}
			upgraded := p
			upgraded.FormatVersion = 3
			upgraded.Data = []byte("new module revision")
			upgraded.SHA256 = ModulePayloadDigest(upgraded.Data)
			if err := db.SaveModulePayload(ctx, "synthetic-b", upgraded, &future); err != nil {
				t.Fatal(err)
			}
			if err := db.SaveModulePayload(ctx, "synthetic-b", p, &future); !errors.Is(err, ErrModulePayloadConflict) {
				t.Fatalf("stale revision overwrote upgrade: %v", err)
			}
			if err := db.DeleteModulePayload(ctx, "synthetic-b", upgraded.ModuleID, upgraded.RecordID, upgraded.Revision()); err != nil {
				t.Fatal(err)
			}
			rows, err = db.ModulePayloads(ctx, "synthetic-b")
			if err != nil || len(rows) != 0 {
				t.Fatal("explicit module retirement failed")
			}
		})
	}
}

func TestModulePayloadBoundsAndInsertOnly(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	compatibilityExec(t, db, `INSERT INTO instances(id,name,created_at) VALUES ('synthetic-a','A','now')`)
	p := ModulePayload{ModuleID: "future.plugin", RecordID: "record", FormatVersion: 1, Data: []byte("original")}
	p.SHA256 = ModulePayloadDigest(p.Data)
	if err := db.SaveModulePayload(context.Background(), "synthetic-a", p, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveModulePayload(context.Background(), "synthetic-a", p, nil); !errors.Is(err, ErrModulePayloadConflict) {
		t.Fatal("insert overwrote existing record")
	}
	if err := db.RestoreModulePayloads(context.Background(), "synthetic-a", []ModulePayload{p}); err == nil {
		t.Fatal("restore accepted occupied target")
	}
	if err := ValidateModulePayloads(make([]ModulePayload, MaxModulePayloadRecords+1)); err == nil {
		t.Fatal("record limit ignored")
	}
	p.Data = make([]byte, MaxModulePayloadBytes+1)
	p.SHA256 = ModulePayloadDigest(p.Data)
	if err := ValidateModulePayloads([]ModulePayload{p}); err == nil {
		t.Fatal("record byte limit ignored")
	}
	// Bound the write path too, so ordinary modules cannot accumulate a
	// database which the protocol's own exporter cannot transport.
	compatibilityExec(t, db, `WITH RECURSIVE records(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM records WHERE n<9999)
		INSERT INTO module_payloads(instance_id,module_id,record_id,format_version,payload_base64,sha256,requires_rebind)
		SELECT 'synthetic-a','future.quota',CAST(n AS TEXT),1,'',?,0 FROM records`, ModulePayloadDigest(nil))
	p.RecordID = "over-quota"
	p.Data = nil
	p.SHA256 = ModulePayloadDigest(nil)
	if err := db.SaveModulePayload(context.Background(), "synthetic-a", p, nil); err == nil {
		t.Fatal("record quota ignored on write")
	}
}

func TestModulePayloadValidationAndRestoreRollback(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	compatibilityExec(t, db, `INSERT INTO instances(id,name,created_at) VALUES ('synthetic-a','A','now')`)
	p := ModulePayload{ModuleID: "future.module", RecordID: "one", FormatVersion: 1, Data: []byte("opaque")}
	p.SHA256 = ModulePayloadDigest(p.Data)
	for _, mutate := range []func(*ModulePayload){func(p *ModulePayload) { p.SHA256 = "broken" }, func(p *ModulePayload) { p.ModuleID = "" }, func(p *ModulePayload) { p.FormatVersion = 0 }} {
		bad := p
		mutate(&bad)
		if err := ValidateModulePayloads([]ModulePayload{bad}); err == nil {
			t.Fatal("invalid payload accepted")
		}
	}
	if err := ValidateModulePayloads([]ModulePayload{p, p}); err == nil {
		t.Fatal("duplicate payload accepted")
	}
	compatibilityExec(t, db, `CREATE TRIGGER reject_payload BEFORE INSERT ON module_payloads WHEN NEW.record_id='two' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
	second := p
	second.RecordID = "two"
	if err := db.RestoreModulePayloads(context.Background(), "synthetic-a", []ModulePayload{p, second}); err == nil {
		t.Fatal("failed restore succeeded")
	}
	rows, err := db.ModulePayloads(context.Background(), "synthetic-a")
	if err != nil || len(rows) != 0 {
		t.Fatal("partial payload restore remained")
	}
}
