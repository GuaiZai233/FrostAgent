// Package futuremodule is an independent future-format fixture binary. It must
// not import storage, backup, or instance: only this producer knows format 9.
package futuremodule

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReleaseProbe(t *testing.T) {
	op := os.Getenv("FROSTAGENT_COMPAT_PROBE")
	if op == "" {
		t.Skip("cross-version driver supplies isolated fixture")
	}
	root, id := os.Getenv("FROSTAGENT_COMPAT_ROOT"), os.Getenv("FROSTAGENT_COMPAT_INSTANCE")
	if root == "" || id == "" {
		t.Fatal("missing isolated fixture")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "frostagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Exact bytes include JSON keys the reader has never seen and non-UTF8
	// binary trailer. Neither parsing/remarshalling nor string conversion is safe.
	want := append([]byte(`{"future_field":{"generation":9},"new_enum":"future-only"}`), 0, 255, 128)
	sum := sha256.Sum256(want)
	digest := hex.EncodeToString(sum[:])
	if op == "seed-module" {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`INSERT INTO module_payloads(instance_id,module_id,record_id,format_version,payload_base64,sha256,requires_rebind) VALUES (?,'future.fixture','record',9,?,?,0)`, id, base64.StdEncoding.EncodeToString(want), digest); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE schema_meta SET version=21,min_compatible_version=20`); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if op == "verify-deleted" {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM module_payloads WHERE instance_id=?`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("orphan records=%d %v", count, err)
		}
		return
	}
	if op != "verify-module" && op != "verify-module-restored" {
		t.Fatalf("unknown future operation %q", op)
	}
	var encoded, actualDigest string
	var format, rebind int
	if err := db.QueryRow(`SELECT format_version,payload_base64,sha256,requires_rebind FROM module_payloads WHERE instance_id=? AND module_id='future.fixture' AND record_id='record'`, id).Scan(&format, &encoded, &actualDigest, &rebind); err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || !bytes.Equal(got, want) || actualDigest != digest || format != 9 {
		t.Fatalf("future record lost or rewritten: %v", err)
	}
	wantRebind := 0
	if op == "verify-module-restored" {
		wantRebind = 1
	}
	if rebind != wantRebind {
		t.Fatalf("binding state=%d want=%d", rebind, wantRebind)
	}
}
