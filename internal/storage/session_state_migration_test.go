package storage

import (
	"context"
	"testing"
)

func TestPostgresMigrationSessionStateV17(t *testing.T) {
	for _, backend := range []Backend{SQLite, Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			root, config, db := compatibilityDatabase(t, backend)
			prepareOldSchema(t, db, 17)
			populateMigrationFixture(t, db)
			compatibilityClose(t, db)
			db = compatibilityReopen(t, root, config)
			defer db.Close()
			assertMigrationFixture(t, db)
			assertCompatibilityVersions(t, db, 18, 18)
			if err := db.validateFutureLayout(context.Background(), db.SQL); err != nil {
				t.Fatal(err)
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
