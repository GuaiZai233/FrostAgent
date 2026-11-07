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

func TestPostgresMigrationSchemaCompatibilityGeneratedColumns(t *testing.T) {
	for _, test := range []struct {
		name, attribute string
		legacy, accept  bool
	}{
		{name: "stored-generated", attribute: "generated"},
		{name: "always-identity", attribute: "identity"},
		{name: "default-identity", attribute: "identity by default"},
		{name: "legacy-v15-no-backfill", attribute: "generated", legacy: true},
		{name: "ordinary-columns", accept: true},
		{name: "independent-extension", accept: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			version := 16
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			switch test.attribute {
			case "generated":
				compatibilityExec(t, db, `ALTER TABLE counters DROP COLUMN value`)
				compatibilityExec(t, db, `ALTER TABLE counters ADD COLUMN value BIGINT GENERATED ALWAYS AS (0) STORED NOT NULL`)
			case "identity", "identity by default":
				mode := "ALWAYS"
				if test.attribute == "identity by default" {
					mode = "BY DEFAULT"
				}
				compatibilityExec(t, db, `ALTER TABLE counters ALTER COLUMN value ADD GENERATED `+mode+` AS IDENTITY`)
			}
			if test.name == "independent-extension" {
				compatibilityExec(t, db, `CREATE TABLE future_generated (id INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
					payload TEXT NOT NULL, size INTEGER GENERATED ALWAYS AS (length(payload)) STORED)`)
				compatibilityExec(t, db, `INSERT INTO future_generated (payload) VALUES ('synthetic-payload')`)
			}
			before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
			if test.attribute == "generated" || test.attribute == "identity" {
				err := db.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73)
				var pgError *pgconn.PgError
				if !errors.As(err, &pgError) || pgError.Code != "428C9" {
					t.Fatalf("real instance writer did not reject generated/identity counter: %v", err)
				}
				if after := compatibilityPostgresSchemaSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("failed instance creation changed data, attributes or metadata")
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, config)
			if test.accept {
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, version, 14)
				if after := compatibilityPostgresSchemaSnapshot(t, opened.SQL); !reflect.DeepEqual(before, after) {
					t.Fatal("compatible startup changed data or generated extension")
				}
				if err := opened.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				opened.Close()
				t.Fatal("generated/identity core writer column was accepted")
			}
			if !strings.Contains(err.Error(), "counters") || !strings.Contains(err.Error(), "value") ||
				(!strings.Contains(err.Error(), "generated") && !strings.Contains(err.Error(), "identity")) {
				t.Fatalf("startup did not identify the column attribute: %v", err)
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed data, generated attributes or metadata")
			}
		})
	}
}
