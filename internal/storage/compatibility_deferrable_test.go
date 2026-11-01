package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresMigrationSchemaCompatibilityDeferrableConstraints(t *testing.T) {
	for _, test := range []struct {
		name, table, clause string
		legacy, accept      bool
	}{
		{name: "primary-initially-deferred", table: "counters", clause: "DEFERRABLE INITIALLY DEFERRED"},
		{name: "primary-initially-immediate", table: "counters", clause: "DEFERRABLE INITIALLY IMMEDIATE"},
		{name: "unique-initially-deferred", table: "instances", clause: "DEFERRABLE INITIALLY DEFERRED"},
		{name: "unique-initially-immediate", table: "instances", clause: "DEFERRABLE INITIALLY IMMEDIATE"},
		{name: "legacy-v15-no-backfill", table: "counters", clause: "DEFERRABLE INITIALLY DEFERRED", legacy: true},
		{name: "primary-not-deferrable", table: "counters", clause: "NOT DEFERRABLE", accept: true},
		{name: "unique-not-deferrable", table: "instances", clause: "NOT DEFERRABLE", accept: true},
		{name: "independent-extension", table: "future_extension", accept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			version := 16
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			switch test.table {
			case "counters":
				compatibilityExec(t, db, `ALTER TABLE counters DROP CONSTRAINT counters_pkey`)
				compatibilityExec(t, db, `ALTER TABLE counters ADD CONSTRAINT counters_pkey PRIMARY KEY (key) `+test.clause)
			case "instances":
				compatibilityExec(t, db, `ALTER TABLE instances DROP CONSTRAINT instances_name_key`)
				compatibilityExec(t, db, `ALTER TABLE instances ADD CONSTRAINT instances_name_key UNIQUE (name) `+test.clause)
			case "future_extension":
				compatibilityExec(t, db, `CREATE TABLE future_extension (
					id TEXT PRIMARY KEY DEFERRABLE INITIALLY DEFERRED,
					payload TEXT NOT NULL UNIQUE DEFERRABLE INITIALLY IMMEDIATE)`)
				compatibilityExec(t, db, `INSERT INTO future_extension VALUES ('synthetic-extension', 'preserve exactly')`)
			}
			before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
			if !test.accept {
				proveCompatibilityDeferrableArbiterFailure(t, db, test.table)
				if after := compatibilityPostgresSchemaSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("failed writer changed rows, metadata or constraints")
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, config)
			if test.accept {
				if err != nil {
					t.Fatalf("compatible constraint layout was rejected: %v", err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, version, 14)
				if after := compatibilityPostgresSchemaSnapshot(t, opened.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("compatible startup changed rows, metadata or constraints")
				}
				if err := opened.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73); err != nil {
					t.Fatalf("compatible instance creation failed: %v", err)
				}
				assertCompatibilityJSON(t, opened)
				if test.table == "future_extension" {
					after := compatibilityPostgresSchemaSnapshot(t, opened.SQL)
					for _, suffix := range []string{":rows", ":columns", ":constraints"} {
						if !reflect.DeepEqual(before[test.table+suffix], after[test.table+suffix]) {
							t.Fatalf("ordinary write changed independent extension%s", suffix)
						}
					}
				}
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("future deferrable core constraint was accepted")
			}
			if !strings.Contains(err.Error(), test.table) || !strings.Contains(strings.ToLower(err.Error()), "deferrable") {
				t.Fatalf("startup did not identify the incompatible deferrable constraint: %v", err)
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed rows, metadata or constraints")
			}
		})
	}
}

func compatibilityArbiterInstance() InstanceRecord {
	return InstanceRecord{
		ID: "synthetic-arbiter-instance", Name: "Synthetic arbiter instance",
		CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
}

func proveCompatibilityDeferrableArbiterFailure(t *testing.T, db *DB, table string) {
	t.Helper()
	var err error
	if table == "counters" {
		// Exercise the real writer: the instance INSERT succeeds, but the
		// counters UPSERT fails and must roll the whole transaction back.
		err = db.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73)
	} else {
		// The current instance writer does not upsert by name. This direct
		// probe demonstrates the same arbiter restriction for a non-PK UNIQUE
		// constraint, independently of the startup compatibility check.
		tx, beginErr := db.SQL.BeginTx(context.Background(), nil)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer tx.Rollback()
		_, err = tx.Exec(`INSERT INTO instances (id, name, created_at)
			VALUES ('synthetic-arbiter-instance', 'Synthetic arbiter instance', '2026-01-01T00:00:00Z')
			ON CONFLICT (name) DO UPDATE SET name = excluded.name`)
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
	}
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55000" || !strings.Contains(strings.ToLower(err.Error()), "deferrable") {
		t.Fatalf("fixture did not reject deferrable ON CONFLICT arbiter: %v", err)
	}
}
