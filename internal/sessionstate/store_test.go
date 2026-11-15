package sessionstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func checkpoint(id string, revision int64) Record {
	return Record{SessionID: id, Revision: revision, Payload: json.RawMessage(`{"version":1}`)}
}

func TestLocalRestartAndReorderedSaves(t *testing.T) {
	dir := t.TempDir()
	s := New(nil, "instance", dir, nil)
	if err := s.Save(checkpoint("private:synthetic", 3)); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(checkpoint("private:synthetic", 2)); err != nil {
		t.Fatal(err)
	}
	next := New(nil, "instance", dir, nil)
	records, err := next.Load(context.Background())
	if err != nil || len(records) != 1 || records[0].Revision != 3 {
		t.Fatalf("restart: %v %v", records, err)
	}
	if next.Status().Pending != 1 {
		t.Fatal("offline checkpoint was not queued for SQL replay")
	}
}

func TestFlushRetainsNewRevisionAndRetriesLocalDisk(t *testing.T) {
	dir := t.TempDir()
	s := New(nil, "instance", filepath.Join(dir, "blocked"), nil)
	if err := os.WriteFile(s.dir, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(checkpoint("private:synthetic", 1)); err == nil {
		t.Fatal("expected local IO failure")
	}
	s.write = func(context.Context, Record) error { return nil }
	if err := s.Flush(context.Background()); err == nil {
		t.Fatal("missing local failure")
	}
	if s.Status().LocalPending != 1 || s.Status().Error == "" {
		t.Fatal("SQL success hid local IO failure")
	}
	if err := os.Remove(s.dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().LocalPending != 0 {
		t.Fatal("local retry did not complete")
	}
	if _, err := s.Read("private:synthetic"); err != nil {
		t.Fatal(err)
	}
	s.write = func(_ context.Context, record Record) error {
		if record.Revision == 2 {
			return s.Save(checkpoint(record.SessionID, 3))
		}
		return nil
	}
	if err := s.Save(checkpoint("private:synthetic", 2)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().Pending != 1 {
		t.Fatal("flush discarded a concurrent newer save")
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLRecoveryUsesNewestLocalRevision(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('instance','Synthetic','now')`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := New(db, "instance", dir, nil)
	if err = s.Save(checkpoint("private:synthetic", 1)); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.write = func(context.Context, Record) error { return errors.New("offline") }
	if err = s.Save(checkpoint("private:synthetic", 2)); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(context.Background()); err == nil {
		t.Fatal("expected SQL outage")
	}
	next := New(db, "instance", dir, nil)
	records, err := next.Load(context.Background())
	if err != nil || len(records) != 1 || records[0].Revision != 2 {
		t.Fatalf("merge: %v %v", records, err)
	}
	if err = next.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = db.SQL.QueryRow(`SELECT revision FROM session_states WHERE instance_id='instance'`).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("SQL replay: %d %v", revision, err)
	}
	if err = s.writeSQL(context.Background(), checkpoint("private:synthetic", 1)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale SQL write was not reported: %v", err)
	}
	if err = db.SQL.QueryRow(`SELECT revision FROM session_states WHERE instance_id='instance'`).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("SQL accepted stale checkpoint")
	}
}

func TestConflictingCheckpointPreservesLocalAlternative(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('instance','Synthetic','now')`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := New(db, "instance", dir, nil)
	local := checkpoint("private:synthetic", 1)
	local.Payload = json.RawMessage(`{"version":1,"content":"local"}`)
	if err := s.Save(local); err != nil {
		t.Fatal(err)
	}
	remote := checkpoint(local.SessionID, 1)
	remote.Payload = json.RawMessage(`{"version":1,"content":"SQL"}`)
	if err := s.writeSQL(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	next := New(db, "instance", dir, nil)
	records, err := next.Load(context.Background())
	if err == nil || len(records) != 1 || string(records[0].Payload) != string(remote.Payload) {
		t.Fatalf("conflict restore: %v %v", records, err)
	}
	files, err := filepath.Glob(next.path(local.SessionID) + ".*.conflict")
	if err != nil || len(files) != 1 {
		t.Fatalf("missing preserved conflict: %v %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	var preserved Record
	if err != nil || json.Unmarshal(data, &preserved) != nil || string(preserved.Payload) != string(local.Payload) {
		t.Fatal("local alternative lost")
	}
	if records, err := New(db, "instance", dir, nil).Load(context.Background()); err == nil || len(records) != 1 {
		t.Fatalf("conflict artifact was replayed or its warning disappeared: %v", err)
	}
}

func checkpointDatabase(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('instance','Synthetic','now')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSQLCommitAcknowledgesOnlyIdenticalRevision(t *testing.T) {
	for _, kind := range []string{"identical", "different", "higher"} {
		t.Run(kind, func(t *testing.T) {
			db := checkpointDatabase(t)
			s := New(db, "instance", t.TempDir(), nil)
			local := checkpoint("private:synthetic", 2)
			remote := local
			switch kind {
			case "identical":
				remote.Payload = json.RawMessage(`{ "version": 1 }`)
			case "different":
				remote.Payload = json.RawMessage(`{"version":1,"branch":"SQL"}`)
			case "higher":
				remote.Revision = 3
			}
			if err := s.writeSQL(context.Background(), remote); err != nil {
				t.Fatal(err)
			}
			if err := s.Save(local); err != nil {
				t.Fatal(err)
			}
			err := s.Flush(context.Background())
			if kind == "identical" {
				if err != nil || s.Status().Pending != 0 {
					t.Fatalf("idempotent retry: %v %+v", err, s.Status())
				}
				return
			}
			if !errors.Is(err, ErrConflict) || s.Status().Pending != 1 || s.Status().Error == "" {
				t.Fatalf("conflict acknowledged or hidden: %v %+v", err, s.Status())
			}
			files, err := filepath.Glob(s.path(local.SessionID) + ".*.conflict")
			if err != nil || len(files) != 2 {
				t.Fatalf("both branches were not preserved: %v %v", files, err)
			}
			// A later local revision must not silently overwrite the other branch.
			local.Revision = 4
			if err := s.Save(local); err != nil {
				t.Fatal(err)
			}
			if err := s.Save(checkpoint("private:peer", 1)); err != nil {
				t.Fatal(err)
			}
			if err := s.Flush(context.Background()); !errors.Is(err, ErrConflict) {
				t.Fatalf("conflicted branch was retried as a normal write: %v", err)
			}
			persisted, err := s.readSQL(context.Background(), local.SessionID)
			if err != nil || persisted.Revision != remote.Revision || !equalPayload(persisted.Payload, remote.Payload) || s.Status().Pending != 1 {
				t.Fatalf("conflict fence or unrelated session flush failed: %v %+v", err, s.Status())
			}
			if _, err := s.readSQL(context.Background(), "private:peer"); err != nil {
				t.Fatal("one conflict blocked another session")
			}
		})
	}
}

func TestSQLDriftAfterLocalFailureAndOfflineRestartPreservesConversation(t *testing.T) {
	db := checkpointDatabase(t)
	dir := t.TempDir()
	s := New(db, "instance", dir, nil)
	first := checkpoint("private:synthetic", 1)
	if err := s.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	stale, err := os.ReadFile(s.path(first.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.path(first.SessionID)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.path(first.SessionID), 0700); err != nil {
		t.Fatal(err)
	}
	remote := checkpoint(first.SessionID, 2)
	remote.Payload = json.RawMessage(`{"version":1,"messages":["committed while local disk failed"]}`)
	if err := s.Save(remote); err == nil {
		t.Fatal("expected local write failure")
	}
	if err := s.Flush(context.Background()); err == nil || s.Status().Pending != 0 || s.Status().LocalPending != 1 {
		t.Fatalf("SQL did not succeed independently of the local failure: %v %+v", err, s.Status())
	}
	if err := os.Remove(s.path(first.SessionID)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path(first.SessionID), stale, 0600); err != nil {
		t.Fatal(err)
	}
	offline := New(nil, "instance", dir, nil)
	if records, err := offline.Load(context.Background()); err != nil || len(records) != 1 || records[0].Revision != 1 {
		t.Fatalf("offline restart did not use the stale mirror: %v %v", records, err)
	}
	local := checkpoint(first.SessionID, 2)
	local.Payload = json.RawMessage(`{"version":1,"messages":["new conversation after offline restart"]}`)
	if err := offline.Save(local); err != nil {
		t.Fatal(err)
	}
	offline.db = db
	if err := offline.Flush(context.Background()); !errors.Is(err, ErrConflict) || offline.Status().Pending != 1 {
		t.Fatalf("revision collision was falsely committed: %v %+v", err, offline.Status())
	}
	if record, err := offline.Read(first.SessionID); err != nil || !equalPayload(record.Payload, local.Payload) {
		t.Fatal("pending conversation was lost")
	}
	restarted := New(db, "instance", dir, nil)
	if _, err := restarted.Load(context.Background()); err == nil || restarted.Status().Error == "" {
		t.Fatal("restart silently resolved the conflict")
	}
	files, err := filepath.Glob(s.path(first.SessionID) + ".*.conflict")
	if err != nil {
		t.Fatal(err)
	}
	preserved := false
	for _, path := range files {
		data, err := os.ReadFile(path)
		var record Record
		if err == nil && json.Unmarshal(data, &record) == nil && equalPayload(record.Payload, local.Payload) {
			preserved = true
		}
	}
	if !preserved {
		t.Fatal("offline conversation was lost across conflict recovery")
	}
}
