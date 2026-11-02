package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresMigrationSchemaCompatibilityExclusionConstraints(t *testing.T) {
	for _, test := range []struct {
		name, clause string
		legacy, safe bool
	}{
		{name: "core-immediate", clause: "NOT DEFERRABLE"},
		{name: "core-deferred", clause: "DEFERRABLE INITIALLY DEFERRED"},
		{name: "legacy-v15-no-backfill", clause: "NOT DEFERRABLE", legacy: true},
		{name: "independent-extension", clause: "NOT DEFERRABLE", safe: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			version := 16
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			table := "instances"
			if test.safe {
				table = "future_extension"
				compatibilityExec(t, db, `CREATE TABLE future_extension (enabled INTEGER NOT NULL, payload TEXT NOT NULL)`)
				compatibilityExec(t, db, `INSERT INTO future_extension VALUES (0, 'synthetic-preserved-payload')`)
			} else {
				compatibilityExec(t, db, `UPDATE instances SET enabled = 0`)
			}
			compatibilityExec(t, db, `ALTER TABLE `+table+` ADD CONSTRAINT future_exclusion
				EXCLUDE USING gist (int4range(enabled, enabled + 1) WITH &&) `+test.clause)
			var unique bool
			if err := db.SQL.QueryRow(`SELECT i.indisunique FROM pg_catalog.pg_constraint c
				JOIN pg_catalog.pg_index i ON i.indexrelid = c.conindid
				WHERE c.conrelid = pg_catalog.to_regclass($1) AND c.contype = 'x'`, table).Scan(&unique); err != nil || unique {
				t.Fatalf("fixture must have an exclusion index invisible to the ordinary unique scan: unique=%v err=%v", unique, err)
			}
			before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
			if !test.safe {
				// Exercise the actual writer, including commit-time checks for the
				// deferred case. The failed transaction must also preserve counters.
				err := db.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73)
				var pgError *pgconn.PgError
				if !errors.As(err, &pgError) || pgError.Code != "23P01" {
					t.Fatalf("fixture did not reject normal instance creation through exclusion: %v", err)
				}
				if after := compatibilityPostgresSchemaSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("failed CreateInstance changed data or constraints")
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, config)
			if test.safe {
				if err != nil {
					t.Fatalf("independent exclusion constraint was rejected: %v", err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, version, 14)
				if after := compatibilityPostgresSchemaSnapshot(t, opened.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("compatible startup changed rows or constraints")
				}
				if err := opened.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73); err != nil {
					t.Fatal(err)
				}
				after := compatibilityPostgresSchemaSnapshot(t, opened.SQL)
				for _, suffix := range []string{":rows", ":columns", ":constraints"} {
					if !reflect.DeepEqual(before[table+suffix], after[table+suffix]) {
						t.Fatalf("instance creation changed independent extension%s", suffix)
					}
				}
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("future core exclusion constraint was accepted")
			}
			if !strings.Contains(err.Error(), table) || !strings.Contains(strings.ToLower(err.Error()), "exclusion") {
				t.Fatalf("startup did not identify the exclusion constraint: %v", err)
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed data, constraints or compatibility metadata")
			}
		})
	}
}
