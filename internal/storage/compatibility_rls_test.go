package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresMigrationSchemaCompatibilityRowSecurity(t *testing.T) {
	for _, test := range []struct {
		name, table                          string
		fallback, legacy, nonOwner, shadowed bool
		disabled, forceOnly                  bool
	}{
		{name: "forced-owner-instances", table: "instances"},
		{name: "non-owner-instances", table: "instances", nonOwner: true},
		{name: "forced-owner-scopes", table: "memory_embedding_scopes"},
		{name: "fallback-scopes", table: "memory_embedding_scopes", fallback: true},
		{name: "legacy-v15-no-backfill", table: "memory_embedding_scopes", fallback: true, legacy: true},
		{name: "shadowed-same-name", table: "memory_embedding_scopes", shadowed: true},
		{name: "disabled-policy", table: "memory_embedding_scopes", disabled: true},
		{name: "force-without-enable", table: "memory_embedding_scopes", forceOnly: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Register role cleanup before fixture schemas, so their objects and
			// ACLs are removed before DROP ROLE. The role is unique per test.
			role := compatibilityUnprivilegedRole(t)
			root, config, db := compatibilityDatabase(t, Postgres)
			compatibilitySeedComputerUse(t, db)
			seedCompatibilityEmbeddingCaches(t, db, false)
			compatibilityExec(t, db, `DROP TABLE memory_embeddings`)
			version := 16
			if test.legacy {
				version = 15
			}
			compatibilityMetadata(t, db, version, !test.legacy, 14)
			var activeSchema string
			if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&activeSchema); err != nil {
				t.Fatal(err)
			}
			moduleSchema, moduleDB := compatibilityModuleSchema(t, config, activeSchema+`.rls"Data`)
			target := compatibilityQualifiedTable(activeSchema, test.table)
			if test.fallback || test.shadowed {
				target = compatibilityQualifiedTable(moduleSchema, test.table)
				if test.shadowed {
					compatibilityExec(t, db, `CREATE TABLE `+target+` AS TABLE `+test.table)
				} else {
					compatibilityExec(t, db, `ALTER TABLE `+test.table+` SET SCHEMA "`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
				}
			}
			for _, schema := range []string{activeSchema, moduleSchema} {
				quotedSchema := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
				compatibilityExec(t, db, `GRANT USAGE ON SCHEMA `+quotedSchema+` TO `+role)
				compatibilityExec(t, db, `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA `+quotedSchema+` TO `+role)
			}
			if !test.nonOwner {
				compatibilityExec(t, db, `ALTER TABLE `+target+` OWNER TO `+role)
			}
			if test.legacy {
				// A valid legacy fixture would be able to backfill the marker.
				compatibilityExec(t, db, `ALTER TABLE schema_meta OWNER TO `+role)
			}
			compatibilityExec(t, db, `CREATE POLICY synthetic_deny ON `+target+` USING (false) WITH CHECK (false)`)
			if !test.forceOnly {
				compatibilityExec(t, db, `ALTER TABLE `+target+` ENABLE ROW LEVEL SECURITY`)
			}
			if !test.nonOwner {
				compatibilityExec(t, db, `ALTER TABLE `+target+` FORCE ROW LEVEL SECURITY`)
			}
			if test.disabled {
				compatibilityExec(t, db, `ALTER TABLE `+target+` DISABLE ROW LEVEL SECURITY`)
			}
			before := compatibilityPostgresSchemaSnapshot(t, db.SQL)
			moduleBefore := compatibilityPostgresSchemaSnapshot(t, moduleDB)
			parsed, err := url.Parse(config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			parsed.User = url.UserPassword(role, "synthetic-test-password")
			query := parsed.Query()
			query.Set("search_path", `"`+strings.ReplaceAll(activeSchema, `"`, `""`)+`","`+strings.ReplaceAll(moduleSchema, `"`, `""`)+`"`)
			parsed.RawQuery = query.Encode()
			roleConfig := Config{Backend: Postgres, DSN: parsed.String()}
			writer, err := sql.Open("pgx", roleConfig.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			var who string
			var privileged, ownsTarget bool
			if err := writer.QueryRow(`SELECT current_user, r.rolsuper OR r.rolbypassrls,
				c.relowner = r.oid FROM pg_catalog.pg_roles r, pg_catalog.pg_class c
				WHERE r.rolname = current_user AND c.oid = $1::regclass`, target).Scan(&who, &privileged, &ownsTarget); err != nil || who != role || privileged || ownsTarget == test.nonOwner {
				t.Fatalf("unexpected app role: user=%q privileged=%v owner=%v err=%v", who, privileged, ownsTarget, err)
			}
			accept := test.shadowed || test.disabled || test.forceOnly
			if !accept {
				app := &DB{SQL: writer, Backend: Postgres}
				if test.table == "instances" {
					records, _, err := app.LoadInstances(context.Background())
					if err != nil || len(records) != 0 {
						t.Fatalf("RLS did not hide the existing instance: count=%d err=%v", len(records), err)
					}
					err = app.CreateInstance(context.Background(), compatibilityArbiterInstance(), 73)
					var pgError *pgconn.PgError
					if !errors.As(err, &pgError) || pgError.Code != "42501" {
						t.Fatalf("RLS did not reject normal instance creation: %v", err)
					}
				} else {
					// This is the memory import scope DELETE, not the full API.
					result, err := writer.Exec(`DELETE FROM memory_embedding_scopes WHERE instance_id = 'synthetic-instance'`)
					if err != nil {
						t.Fatal(err)
					}
					count, err := result.RowsAffected()
					if err != nil || count != 0 {
						t.Fatalf("RLS did not silently suppress scope deletion: count=%d err=%v", count, err)
					}
				}
			}
			compatibilityClose(t, db)
			opened, err := OpenWithConfig(context.Background(), root, roleConfig)
			if accept {
				if err != nil {
					t.Fatalf("safe RLS layout was rejected: %v", err)
				}
				defer opened.Close()
				assertCompatibilityVersions(t, opened, version, 14)
			} else {
				if err == nil {
					opened.Close()
					t.Fatal("enabled row-level security on a writer target was accepted")
				}
				if !strings.Contains(err.Error(), "row-level security") {
					t.Fatalf("startup did not identify row-level security: %v", err)
				}
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := compatibilityPostgresSchemaSnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("startup or rejected writes changed active data, defaults, RLS or metadata")
			}
			if after := compatibilityPostgresSchemaSnapshot(t, moduleDB); !reflect.DeepEqual(moduleBefore, after) {
				t.Fatal("startup or rejected writes changed external data or RLS")
			}
		})
	}
}

func compatibilityUnprivilegedRole(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset; CI supplies a disposable role-capable database")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	role := fmt.Sprintf("fa_compat_rls_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE ROLE ` + role + ` LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'synthetic-test-password'`); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP ROLE ` + role); err != nil {
			t.Errorf("remove disposable RLS role: %v", err)
		}
		admin.Close()
	})
	return role
}
