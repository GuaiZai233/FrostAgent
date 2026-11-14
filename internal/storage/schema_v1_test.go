package storage

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestV1CleanupMigration(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			fresh := map[string][]schemaColumn{}
			for _, table := range []string{"dialogues", "memory_entries"} {
				fresh[table], _ = db.tableColumns(context.Background(), db.SQL, table)
			}
			prepareOldSchema(t, db, 10)
			seedDuplicateProviders(t, db)
			for _, step := range migrations {
				if step.version == 11 {
					if err := db.applyMigration(context.Background(), step); err != nil {
						t.Fatal(err)
					}
				}
			}
			// The live catalog has diverged from the retained v10 archives.
			compatibilityExec(t, db, `UPDATE provider_endpoints SET display_name='live name'`)
			compatibilityExec(t, db, `INSERT INTO dialogues(instance_id,position,id,scene,relation,user_text,preferred) VALUES ('synthetic-a',0,'example','retired-scene','retired-relation','question','answer')`)
			compatibilityExec(t, db, `INSERT INTO memory_entries(instance_id,scope_type,id,owner,content,source,visibility,created_at,updated_at) VALUES ('synthetic-a','private','memory','synthetic-user','current memory','manual','retired-public','now','now')`)
			compatibilityClose(t, db)
			db, err := OpenWithConfig(context.Background(), root, config)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			assertMigratedProviders(t, db)
			assertCompatibilityVersions(t, db, 18, 18)
			for _, table := range []string{"dialogues", "memory_entries"} {
				columns, err := db.tableColumns(context.Background(), db.SQL, table)
				if err != nil || !reflect.DeepEqual(columns, fresh[table]) {
					t.Fatalf("fresh/upgraded %s differ: %v", table, err)
				}
			}
			for _, table := range []string{"models", "model_endpoints", "model_secrets", "endpoint_ids"} {
				columns, err := db.tableColumns(context.Background(), db.SQL, table)
				if err != nil || len(columns) != 0 {
					t.Fatalf("retired table %s remains: %v", table, err)
				}
			}
			for query, want := range map[string]string{
				`SELECT display_name FROM provider_endpoints LIMIT 1`: "live name",
				`SELECT user_text FROM dialogues`:                     "question",
				`SELECT content FROM memory_entries`:                  "current memory",
			} {
				var got string
				if err := db.SQL.QueryRow(query).Scan(&got); err != nil || got != want {
					t.Fatalf("current data changed: %q %v", got, err)
				}
			}
		})
	}
}

func TestV1CleanupRollsBack(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			_, _, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, 17)
			populateMigrationFixture(t, db)
			// Recreate populated archives as retained by an upgraded v17 database.
			for _, statement := range schemaStatements {
				for _, table := range []string{"endpoint_ids", "model_endpoints", "model_secrets", "models"} {
					if strings.HasPrefix(statement, "CREATE TABLE "+table+" (") {
						compatibilityExec(t, db, statement)
					}
				}
			}
			for _, statement := range []string{
				`INSERT INTO endpoint_ids VALUES ('retired-endpoint','synthetic-instance')`,
				`INSERT INTO model_endpoints(instance_id,id,display_name,base_url,api_key_source,enabled,position) VALUES ('synthetic-instance','retired-endpoint','Retired','https://retired.invalid','sql',1,0)`,
				`INSERT INTO model_secrets VALUES ('synthetic-instance','retired-endpoint','synthetic-retired-secret')`,
				`INSERT INTO models VALUES ('synthetic-instance','retired-model','Retired','retired-endpoint','synthetic-model',1,'{}',0)`,
				`INSERT INTO dialogues(instance_id,position,id,scene,relation,user_text,preferred) VALUES ('synthetic-instance',0,'synthetic-dialogue','retired-scene','retired-relation','question','answer')`,
				`UPDATE memory_entries SET visibility='retired-value'`,
			} {
				compatibilityExec(t, db, statement)
			}
			// Fail at the final version update, after every DROP and the module
			// table/registration have executed, not in the pre-migration guard.
			if backend == Postgres {
				compatibilityExec(t, db, `CREATE FUNCTION fail_v1_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic v1 final update failure'; END $$`)
				compatibilityExec(t, db, `CREATE TRIGGER fail_v1 BEFORE UPDATE ON schema_meta FOR EACH ROW EXECUTE FUNCTION fail_v1_update()`)
			} else {
				compatibilityExec(t, db, `CREATE TRIGGER fail_v1 BEFORE UPDATE ON schema_meta BEGIN SELECT RAISE(ABORT,'synthetic v1 final update failure'); END`)
			}
			before := compatibilitySnapshot(t, db.SQL, backend)
			err := db.applyMigration(context.Background(), migrations[len(migrations)-1])
			if err == nil || !strings.Contains(err.Error(), "synthetic v1 final update failure") {
				t.Fatalf("cleanup did not reach the injected final update failure: %v", err)
			}
			if after := compatibilitySnapshot(t, db.SQL, backend); !reflect.DeepEqual(before, after) {
				t.Fatal("failed cleanup changed tables, columns, data, module registration or version")
			}
			assertCompatibilityVersions(t, db, 17, 17)
			assertMigrationFixture(t, db)
			if backend == Postgres {
				compatibilityExec(t, db, `DROP TRIGGER fail_v1 ON schema_meta`)
				compatibilityExec(t, db, `DROP FUNCTION fail_v1_update()`)
			} else {
				compatibilityExec(t, db, `DROP TRIGGER fail_v1`)
			}
			if err := db.applyMigration(context.Background(), migrations[len(migrations)-1]); err != nil {
				t.Fatal(err)
			}
			assertCompatibilityVersions(t, db, 18, 18)
			assertMigrationFixture(t, db)
			if err := db.RequireNativeSchema(context.Background(), "export after cleanup retry"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestV1CleanupProtectsUnknownDependentTable(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			_, _, db := compatibilityDatabase(t, backend)
			defer db.Close()
			prepareOldSchema(t, db, 17)
			compatibilityExec(t, db, `INSERT INTO instances(id,name,created_at) VALUES ('synthetic-a','A','now')`)
			compatibilityExec(t, db, `CREATE TABLE endpoint_ids(id TEXT PRIMARY KEY,instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE)`)
			compatibilityExec(t, db, `INSERT INTO endpoint_ids VALUES ('synthetic-endpoint','synthetic-a')`)
			compatibilityExec(t, db, `CREATE TABLE unknown_dependency(id TEXT PRIMARY KEY,endpoint_id TEXT NOT NULL REFERENCES endpoint_ids(id) ON DELETE CASCADE)`)
			compatibilityExec(t, db, `INSERT INTO unknown_dependency VALUES ('synthetic-unknown','synthetic-endpoint')`)
			before := compatibilitySnapshot(t, db.SQL, backend)
			if err := db.applyMigration(context.Background(), migrations[len(migrations)-1]); err == nil {
				t.Fatal("cleanup accepted unknown dependent data")
			}
			if after := compatibilitySnapshot(t, db.SQL, backend); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected cleanup changed data")
			}
		})
	}
}
