package storage

import (
	"context"
	"fmt"
	"testing"
)

func TestPostgresMigrationSessionState(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		for _, version := range []int{17, 18} {
			t.Run(fmt.Sprintf("%s/v%d", backend, version), func(t *testing.T) {
				root, config, db := compatibilityDatabase(t, backend)
				prepareOldSchema(t, db, version)
				populateMigrationFixture(t, db)
				if version == 18 {
					compatibilityExec(t, db, `INSERT INTO tool_history VALUES ('synthetic-instance','synthetic-reference','qq','private','synthetic-user','synthetic-message',1,'{"preserved":true}')`)
				}
				compatibilityClose(t, db)

				db = compatibilityReopen(t, root, config)
				defer db.Close()
				assertMigrationFixture(t, db)
				assertCompatibilityVersions(t, db, SchemaVersion, SchemaVersion)
				if err := db.validateFutureLayout(context.Background(), db.SQL); err != nil {
					t.Fatal(err)
				}
				if version == 18 {
					var payload string
					if err := db.SQL.QueryRow(`SELECT record_json FROM tool_history WHERE ref_id='synthetic-reference'`).Scan(&payload); err != nil || payload != `{"preserved":true}` {
						t.Fatalf("v18 tool history changed: %q %v", payload, err)
					}
				}
				compatibilityExec(t, db, `INSERT INTO session_states(instance_id,session_id,revision,payload) VALUES ('synthetic-instance','private:synthetic',1,'{}')`)
				compatibilityExec(t, db, `DELETE FROM instances WHERE id='synthetic-instance'`)
				var count int
				if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM session_states`).Scan(&count); err != nil || count != 0 {
					t.Fatal("session checkpoint FK did not cascade")
				}
			})
		}
	}
}
