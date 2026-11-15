package sessionstate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	if err != nil || len(files) != 2 {
		t.Fatalf("missing preserved conflict branches: %v %v", files, err)
	}
	foundLocal, foundSQL := false, false
	for _, file := range files {
		data, err := os.ReadFile(file)
		var preserved Record
		if err != nil || json.Unmarshal(data, &preserved) != nil {
			t.Fatal("invalid preserved conflict")
		}
		foundLocal = foundLocal || equalPayload(preserved.Payload, local.Payload)
		foundSQL = foundSQL || equalPayload(preserved.Payload, remote.Payload)
	}
	if !foundLocal || !foundSQL {
		t.Fatal("a conflict alternative was lost")
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

func TestConflictFenceSurvivesRestartAndWorkerFlush(t *testing.T) {
	for _, startup := range []string{"online", "offline", "corrupt-metadata"} {
		t.Run(startup, func(t *testing.T) {
			db := checkpointDatabase(t)
			dir := t.TempDir()
			store := New(db, "instance", dir, nil)
			remote := checkpoint("private:synthetic", 2)
			remote.Payload = json.RawMessage(`{"version":1,"branch":"SQL A"}`)
			if err := store.writeSQL(context.Background(), remote); err != nil {
				t.Fatal(err)
			}
			local := checkpoint(remote.SessionID, 2)
			local.Payload = json.RawMessage(`{"version":1,"branch":"local B"}`)
			if err := store.Save(local); err != nil {
				t.Fatal(err)
			}
			if err := store.Flush(context.Background()); !errors.Is(err, ErrConflict) {
				t.Fatalf("initial conflict: %v", err)
			}
			local.Revision = 3
			local.Payload = json.RawMessage(`{"version":1,"branch":"local B with another message"}`)
			if err := store.Save(local); err != nil {
				t.Fatal(err)
			}
			if startup == "corrupt-metadata" {
				files, err := filepath.Glob(store.path(local.SessionID) + ".*.conflict")
				if err != nil || len(files) == 0 {
					t.Fatalf("conflict files: %v %v", files, err)
				}
				for _, file := range files {
					// A bad payload must not transfer the fence to another ID.
					forged := checkpoint("private:peer", 2)
					data, err := json.Marshal(forged)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			reports := make(chan string, 1)
			restarted := New(db, "instance", dir, func(message string) {
				select {
				case reports <- message:
				default:
				}
			})
			if startup == "offline" {
				restarted.db = nil
			}
			if records, err := restarted.Load(context.Background()); err == nil || len(records) != 1 || records[0].Revision != 3 {
				t.Fatalf("restart did not retain the newer local branch: %v %v", records, err)
			}
			restarted.db = db
			if err := restarted.Save(checkpoint("private:peer", 1)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); restarted.Run(ctx) }()
			select {
			case <-reports:
			case <-time.After(3 * time.Second):
			}
			cancel()
			<-done
			persisted, err := restarted.readSQL(context.Background(), local.SessionID)
			if err != nil || persisted.Revision != remote.Revision || !equalPayload(persisted.Payload, remote.Payload) {
				t.Fatalf("worker overwrote the SQL branch after restart: %+v %v", persisted, err)
			}
			if pending, err := restarted.Read(local.SessionID); err != nil || pending.Revision != 3 || !equalPayload(pending.Payload, local.Payload) || restarted.Status().Pending != 1 {
				t.Fatalf("newer pending conversation was lost: %+v %v %+v", pending, err, restarted.Status())
			}
			if _, err := restarted.readSQL(context.Background(), "private:peer"); err != nil {
				t.Fatal("one restored conflict blocked an unrelated session")
			}
			local.Revision = 4
			if err := restarted.Save(local); err != nil {
				t.Fatal(err)
			}
			if err := restarted.Flush(context.Background()); !errors.Is(err, ErrConflict) {
				t.Fatalf("later local revision escaped the restored fence: %v", err)
			}
		})
	}
}

func TestConflictFenceMustPersistBeforeLocalRevisionAdvances(t *testing.T) {
	db := checkpointDatabase(t)
	dir := t.TempDir()
	store := New(db, "instance", dir, nil)
	remote := checkpoint("private:synthetic", 2)
	remote.Payload = json.RawMessage(`{"version":1,"branch":"SQL A"}`)
	if err := store.writeSQL(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	local := checkpoint(remote.SessionID, 2)
	if err := store.Save(local); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatalf("initial conflict: %v", err)
	}
	files, err := filepath.Glob(store.path(local.SessionID) + ".*.conflict")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("%s.%x.conflict", store.path(remote.SessionID), sha256.Sum256(data))
	if err := os.Mkdir(marker, 0700); err != nil {
		t.Fatal(err)
	}
	local.Revision = 3
	if err := store.Save(local); err == nil || store.Status().LocalPending != 1 {
		t.Fatalf("local mirror advanced without a durable conflict marker: %v %+v", err, store.Status())
	}
	data, err = os.ReadFile(store.path(local.SessionID))
	var mirror Record
	if err != nil || json.Unmarshal(data, &mirror) != nil || mirror.Revision != 2 {
		t.Fatal("failed marker write replaced the old mirror")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background()); !errors.Is(err, ErrConflict) || store.Status().LocalPending != 0 {
		t.Fatalf("marker/local retry: %v %+v", err, store.Status())
	}
	restarted := New(db, "instance", dir, nil)
	if records, err := restarted.Load(context.Background()); err == nil || len(records) != 1 || records[0].Revision != 3 {
		t.Fatalf("retry did not durably preserve the local branch: %v %v", records, err)
	}
	if err := restarted.Flush(context.Background()); !errors.Is(err, ErrConflict) || restarted.Status().Pending != 1 {
		t.Fatalf("retry did not durably preserve the fence: %v %+v", err, restarted.Status())
	}
}

func TestSQLRecoveryChecksAncestryAfterSeveralOfflineSaves(t *testing.T) {
	testSQLRecoveryChecksAncestry(t, checkpointDatabase)
}

func testSQLRecoveryChecksAncestry(t *testing.T, newDB func(*testing.T) *storage.DB) {
	for _, fork := range []bool{false, true} {
		t.Run(fmt.Sprint(fork), func(t *testing.T) {
			db := newDB(t)
			dir := t.TempDir()
			seed := New(db, "instance", dir, nil)
			remote := checkpoint("private:synthetic", 2)
			remote.Payload = json.RawMessage(`{"version":1,"history":"SQL A"}`)
			if err := seed.writeSQL(context.Background(), remote); err != nil {
				t.Fatal(err)
			}
			if fork {
				if err := New(nil, "instance", dir, nil).Save(checkpoint(remote.SessionID, 1)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := seed.Load(context.Background()); err != nil {
				t.Fatal(err)
			}
			offline := New(nil, "instance", dir, nil)
			if _, err := offline.Load(context.Background()); err != nil {
				t.Fatal(err)
			}
			for revision := int64(2); revision <= 4; revision++ {
				if !fork && revision == 2 {
					continue
				}
				local := checkpoint(remote.SessionID, revision)
				local.Payload = json.RawMessage(fmt.Sprintf(`{"version":1,"history":"local continuation %d"}`, revision))
				if err := offline.Save(local); err != nil {
					t.Fatal(err)
				}
			}
			// Pending coalesces to revision 4 before SQL ever sees the local fork.
			offline.db = db
			if err := offline.Save(checkpoint("private:peer", 1)); err != nil {
				t.Fatal(err)
			}
			err := offline.Flush(context.Background())
			persisted, readErr := offline.readSQL(context.Background(), remote.SessionID)
			if fork {
				if !errors.Is(err, ErrConflict) || readErr != nil || persisted.Revision != 2 || !equalPayload(persisted.Payload, remote.Payload) || offline.Status().Pending != 1 {
					t.Fatalf("coalesced fork overwrote SQL: %v %+v %v %+v", err, persisted, readErr, offline.Status())
				}
			} else if err != nil || readErr != nil || persisted.Revision != 4 || offline.Status().Pending != 0 {
				t.Fatalf("legitimate offline continuation failed: %v %+v %v", err, persisted, readErr)
			}
			if _, err := offline.readSQL(context.Background(), "private:peer"); err != nil {
				t.Fatal("one fork blocked the other session")
			}
		})
	}
}

func TestSQLAncestryProtectsRestartBeforeInflightConflictReturns(t *testing.T) {
	testSQLInflightConflictAncestry(t, checkpointDatabase)
}

func testSQLInflightConflictAncestry(t *testing.T, newDB func(*testing.T) *storage.DB) {
	db := newDB(t)
	dir := t.TempDir()
	seed := New(db, "instance", dir, nil)
	remote := checkpoint("private:synthetic", 2)
	remote.Payload = json.RawMessage(`{"version":1,"history":"SQL A"}`)
	if err := seed.writeSQL(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	local := New(nil, "instance", dir, nil)
	for revision := int64(1); revision <= 2; revision++ {
		if err := local.Save(checkpoint(remote.SessionID, revision)); err != nil {
			t.Fatal(err)
		}
	}
	local.db = db
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	local.write = func(ctx context.Context, record Record) error {
		close(entered)
		<-release
		return local.writeSQL(ctx, record)
	}
	done := make(chan error, 1)
	go func() { done <- local.Flush(context.Background()) }()
	<-entered
	for revision := int64(3); revision <= 4; revision++ {
		if err := local.Save(checkpoint(remote.SessionID, revision)); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate restart before the in-flight write can establish a conflict fence.
	restarted := New(db, "instance", dir, nil)
	_, _ = restarted.Load(context.Background())
	err := restarted.Flush(context.Background())
	persisted, readErr := restarted.readSQL(context.Background(), remote.SessionID)
	unblock()
	originalErr := <-done
	if !errors.Is(err, ErrConflict) || !errors.Is(originalErr, ErrConflict) || readErr != nil || persisted.Revision != remote.Revision || !equalPayload(persisted.Payload, remote.Payload) {
		t.Fatalf("restart bypassed the unresolved in-flight fork: %v %v %+v %v", err, originalErr, persisted, readErr)
	}
	if local.Status().Pending != 1 || restarted.Status().Pending != 1 {
		t.Fatal("a concurrent newer local state was discarded")
	}
}

func TestSQLAncestryRetainsConcurrentAndUncertainCommits(t *testing.T) {
	testSQLConcurrentCommitAncestry(t, checkpointDatabase)
}

func testSQLConcurrentCommitAncestry(t *testing.T, newDB func(*testing.T) *storage.DB) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			db := newDB(t)
			dir := t.TempDir()
			store := New(db, "instance", dir, nil)
			if err := store.Save(checkpoint("private:synthetic", 1)); err != nil {
				t.Fatal(err)
			}
			store.write = func(ctx context.Context, record Record) error {
				if record.Revision == 1 {
					if err := store.Save(checkpoint(record.SessionID, 2)); err != nil {
						return err
					}
					if err := store.Save(checkpoint(record.SessionID, 3)); err != nil {
						return err
					}
				}
				err := store.writeSQL(ctx, record)
				if uncertain && err == nil {
					return context.DeadlineExceeded
				}
				return err
			}
			err := store.Flush(context.Background())
			if (err != nil) != uncertain || store.Status().Pending != 1 {
				t.Fatalf("in-flight save was lost: %v %+v", err, store.Status())
			}
			restarted := New(db, "instance", dir, nil)
			if records, err := restarted.Load(context.Background()); err != nil || len(records) != 1 || records[0].Revision != 3 {
				t.Fatalf("in-flight ancestry was not durable: %v %v", records, err)
			}
			if err := restarted.Flush(context.Background()); err != nil || restarted.Status().Pending != 0 {
				t.Fatalf("valid in-flight continuation failed: %v %+v", err, restarted.Status())
			}
		})
	}
}

func postgresCheckpointDatabase(t *testing.T) *storage.DB {
	t.Helper()
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("PostgreSQL test DSN must be a postgres URI")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("fa_session_ancestry_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	db, err := storage.OpenWithConfig(context.Background(), t.TempDir(), storage.Config{Backend: storage.Postgres, DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('instance','Synthetic','now')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPostgresSessionStateAncestry(t *testing.T) {
	t.Run("offline saves", func(t *testing.T) { testSQLRecoveryChecksAncestry(t, postgresCheckpointDatabase) })
	t.Run("inflight conflict", func(t *testing.T) { testSQLInflightConflictAncestry(t, postgresCheckpointDatabase) })
	t.Run("concurrent and uncertain commit", func(t *testing.T) { testSQLConcurrentCommitAncestry(t, postgresCheckpointDatabase) })
	t.Run("whitespace", func(t *testing.T) { testSQLAncestryExactText(t, postgresCheckpointDatabase) })
	t.Run("concurrent SQL writer", testPostgresSQLAncestryCAS)
}

func TestSQLAncestryCASUsesExactStoredJSON(t *testing.T) {
	testSQLAncestryExactText(t, checkpointDatabase)
}

func testSQLAncestryExactText(t *testing.T, newDB func(*testing.T) *storage.DB) {
	db := newDB(t)
	store := New(db, "instance", t.TempDir(), nil)
	for revision := int64(1); revision <= 2; revision++ {
		if err := store.Save(checkpoint("private:synthetic", revision)); err != nil {
			t.Fatal(err)
		}
		if revision == 2 {
			if _, err := db.SQL.Exec(db.Bind(`UPDATE session_states SET payload=? WHERE instance_id='instance'`), "{ \n \"version\": 1 }"); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Flush(context.Background()); err != nil {
			t.Fatalf("equivalent SQL text broke CAS: %v", err)
		}
	}
}

func TestLocalMirrorCannotChangeKnownAncestry(t *testing.T) {
	db := checkpointDatabase(t)
	store := New(db, "instance", t.TempDir(), nil)
	original := checkpoint("private:synthetic", 1)
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	altered := original
	altered.Payload = json.RawMessage(`{"version":1,"history":"unexpected replacement"}`)
	data, err := json.Marshal(altered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(original.SessionID), data, 0600); err != nil {
		t.Fatal(err)
	}
	if restored, err := store.Read(original.SessionID); err != nil || !equalPayload(restored.Payload, original.Payload) {
		t.Fatalf("a replaced mirror supplied the next ancestry base: %+v %v", restored, err)
	}
}

func testPostgresSQLAncestryCAS(t *testing.T) {
	db := postgresCheckpointDatabase(t)
	var schema string
	if err := db.SQL.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	// The external writer/observer use ordinary test connections. The app's
	// connection retains its exclusive lease and single-connection pool.
	external, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	external.SetMaxOpenConns(2)
	store := New(db, "instance", t.TempDir(), nil)
	if err := store.Save(checkpoint("private:synthetic", 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	for revision := int64(2); revision <= 3; revision++ {
		if err := store.Save(checkpoint("private:synthetic", revision)); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := external.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var lockedRevision int64
	if err := tx.QueryRow(`SELECT revision FROM session_states WHERE instance_id='instance' AND session_id='private:synthetic' FOR UPDATE`).Scan(&lockedRevision); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- store.Flush(ctx) }()
	observed := false
	for ctx.Err() == nil {
		var waiting int
		err = external.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity
			WHERE application_name = current_setting('application_name') AND wait_event_type = 'Lock'
			AND query LIKE 'UPDATE session_states SET revision = %'`).Scan(&waiting)
		if err != nil {
			break
		}
		if waiting > 0 {
			observed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !observed {
		tx.Rollback()
		<-done
		t.Fatalf("writer did not reach CAS while the prior SQL row was locked: %v", err)
	}
	other := `{"version":1,"branch":"concurrent SQL update"}`
	if _, err := tx.Exec(db.Bind(`UPDATE session_states SET revision=2,payload=? WHERE instance_id='instance' AND session_id='private:synthetic'`), other); err != nil {
		tx.Rollback()
		<-done
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		<-done
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent SQL update was not rechecked: %v", err)
	}
	persisted, err := store.readSQL(context.Background(), "private:synthetic")
	if err != nil || persisted.Revision != 2 || !equalPayload(persisted.Payload, json.RawMessage(other)) || store.Status().Pending != 1 {
		t.Fatalf("CAS overwrote an unseen SQL branch: %+v %v %+v", persisted, err, store.Status())
	}
}

func TestAncestryHashMatchesSerializedMirror(t *testing.T) {
	db := checkpointDatabase(t)
	store := New(db, "instance", t.TempDir(), nil)
	original := checkpoint("private:synthetic", 1)
	original.Payload = json.RawMessage(`{ "version": 1, "content": "<tag>&data" }`)
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.db = nil
	if record, err := store.Read(original.SessionID); err != nil || !equalPayload(record.Payload, original.Payload) {
		t.Fatalf("local JSON encoding changed its ancestry identity: %+v %v", record, err)
	}
	restarted := New(db, "instance", store.dir, nil)
	if _, err := restarted.Load(context.Background()); err != nil {
		t.Fatalf("equivalent SQL/local JSON became a fork: %v", err)
	}
}
