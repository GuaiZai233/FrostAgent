package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestReadSnapshotKeepsCoreAndPayloadConsistent(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			defer db.Close()
			compatibilityExec(t, db, `INSERT INTO instances(id,name,created_at) VALUES ('synthetic-a','before','now')`)
			p := ModulePayload{ModuleID: "future.plugin", RecordID: "one", FormatVersion: 1, Data: []byte("before")}
			p.SHA256 = ModulePayloadDigest(p.Data)
			if err := db.SaveModulePayload(context.Background(), "synthetic-a", p, nil); err != nil {
				t.Fatal(err)
			}
			driver, dsn := "sqlite", filepath.Join(root, "frostagent.db")
			if backend == Postgres {
				driver, dsn = "pgx", config.DSN
			}
			writer, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			err = db.ReadSnapshot(context.Background(), func(view *DB) error {
				if err := view.SQL.PingContext(context.Background()); err != nil {
					return err
				}
				var name string
				if err := view.SQL.QueryRow(`SELECT name FROM instances WHERE id='synthetic-a'`).Scan(&name); err != nil {
					return err
				}
				if name != "before" {
					t.Fatal("unexpected initial name")
				}
				tx, err := writer.Begin()
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := tx.Exec(`UPDATE instances SET name='after' WHERE id='synthetic-a'`); err != nil {
					return err
				}
				if _, err := tx.Exec(db.Bind(`UPDATE module_payloads SET payload_base64='YWZ0ZXI=',sha256=?`), ModulePayloadDigest([]byte("after"))); err != nil {
					return err
				}
				if err := tx.Commit(); err != nil {
					return err
				}
				rows, err := view.ModulePayloads(context.Background(), "synthetic-a")
				if err != nil {
					return err
				}
				if len(rows) != 1 || string(rows[0].Data) != "before" {
					t.Fatal("snapshot mixed core and payload revisions")
				}
				if _, err := view.SQL.Exec(`DELETE FROM instances`); err == nil {
					t.Fatal("snapshot accepted a write")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := db.ModulePayloads(context.Background(), "synthetic-a")
			if err != nil || len(rows) != 1 || string(rows[0].Data) != "after" {
				t.Fatal("writer update did not persist")
			}
		})
	}
}
