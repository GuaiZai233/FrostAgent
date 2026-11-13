package chathistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/google/uuid"
)

func historyDB(t *testing.T, backend storage.Backend) (*storage.DB, string, storage.Config) {
	t.Helper()
	root, cfg := t.TempDir(), storage.Config{Backend: backend}
	if backend == storage.Postgres {
		base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
		if base == "" {
			t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
		}
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		schema := "fa_history_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		admin, err := sql.Open("pgx", base)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
			if err != nil {
				t.Error(err)
			}
			admin.Close()
		})
		q := parsed.Query()
		q.Set("search_path", schema)
		parsed.RawQuery = q.Encode()
		cfg.DSN = parsed.String()
	}
	db, err := storage.OpenWithConfig(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"fixture-instance", "fixture-other"} {
		if err := db.CreateInstance(context.Background(), storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 3); err != nil {
			t.Fatal(err)
		}
	}
	return db, root, cfg
}

func fixtureRecord(id, content string) Record {
	return Record{Scope: Scope{"qq", "private", "fixture-user"}, ID: id, MessageID: id,
		SenderID: "fixture-user", SenderName: "Fixture", Role: "user", Content: content,
		OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), DeliveryStatus: "received"}
}

func TestRecordingPolicy(t *testing.T) {
	cfg := map[string]string{}
	get := func(k string) string { return cfg[k] }
	private := Scope{"onebot", "private", "fixture-user"}
	group := Scope{"qq", "group", "fixture-group"}
	if !RecordingAllowed(get, private) || RecordingAllowed(get, group) {
		t.Fatal("private on, groups off must be the defaults")
	}
	cfg[PrivateEnabledKey] = "false"
	if RecordingAllowed(get, private) {
		t.Fatal("private disable ignored")
	}
	cfg[GroupWhitelistKey] = "fixture-group,telegram:group:fixture-other\nonebot:group:fixture-alias"
	for _, scope := range []Scope{group, {"telegram", "group", "fixture-other"}, {"qq", "group", "fixture-alias"}} {
		if !RecordingAllowed(get, scope) {
			t.Fatalf("not allowed: %#v", scope)
		}
	}
	for _, scope := range []Scope{{"telegram", "group", "fixture-group"}, {"qq", "group", "fixture-other"}, {"qq", "group", ""}, {"qq", "notice", "fixture-group"}} {
		if RecordingAllowed(get, scope) {
			t.Fatalf("incorrectly allowed: %#v", scope)
		}
	}
	cfg[GroupWhitelistKey] = ""
	if RecordingAllowed(get, group) {
		t.Fatal("removed group still collected")
	}
	for _, value := range []string{"qq:private:fixture-user", ":group:fixture-group", "qq:group:", "bad value"} {
		if ValidateWhitelist(value) == nil {
			t.Fatalf("accepted invalid whitelist %q", value)
		}
	}
}

func TestStoreKeywordIsolationAndRestart(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db, root, cfg := historyDB(t, backend)
			ctx := context.Background()
			store := NewStore(db, "fixture-instance")
			first := fixtureRecord("fixture-first", "你好 FrostAgent 100% a_b ! literal")
			second := fixtureRecord("fixture-second", "你好 later")
			second.OccurredAt = second.OccurredAt.Add(time.Second)
			for _, r := range []Record{first, second} {
				if err := store.Save(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			changed := first
			changed.Content = "must not overwrite original"
			if err := store.Save(ctx, changed); err != nil {
				t.Fatal(err)
			}
			for i, scope := range []Scope{{"telegram", "private", "fixture-user"}, {"qq", "group", "fixture-user"}, {"qq", "private", "fixture-foreign"}} {
				r := fixtureRecord(fmt.Sprintf("foreign-%d", i), first.Content)
				r.Scope = scope
				if err := store.Save(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			if err := NewStore(db, "fixture-other").Save(ctx, first); err != nil {
				t.Fatal(err)
			}
			assert := func(q Query, ids ...string) {
				t.Helper()
				got, err := store.Search(ctx, Scope{"aiocqhttp", "private", "fixture-user"}, q)
				if err != nil || len(got) != len(ids) {
					t.Fatalf("query %#v: got %#v, %v; want %v", q, got, err, ids)
				}
				for i := range ids {
					if got[i].ID != ids[i] {
						t.Fatalf("order: got %s want %s", got[i].ID, ids[i])
					}
				}
			}
			assert(Query{Keywords: []string{"你好"}}, second.ID, first.ID)
			assert(Query{Keywords: []string{"你好", "FROSTAGENT"}}, first.ID)
			assert(Query{Keywords: []string{"later", "FROSTAGENT"}, Match: "any"}, second.ID, first.ID)
			for _, keyword := range []string{"100%", "a_b", "!"} {
				assert(Query{Keywords: []string{keyword}}, first.ID)
			}
			assert(Query{Keywords: []string{"你好"}, After: second.OccurredAt}, second.ID)
			assert(Query{Keywords: []string{"你好"}, Before: second.OccurredAt}, first.ID)
			assert(Query{Keywords: []string{"你好"}, Limit: 1}, second.ID)
			for _, q := range []Query{{}, {Keywords: []string{" "}}, {Keywords: []string{"x"}, Match: "vector"}, {Keywords: []string{"x"}, After: second.OccurredAt, Before: first.OccurredAt}} {
				if _, err := store.Search(ctx, first.Scope, q); err == nil {
					t.Fatalf("accepted query %#v", q)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := storage.OpenWithConfig(ctx, root, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			got, err := NewStore(reopened, "fixture-instance").Search(ctx, first.Scope, Query{Keywords: []string{"100%"}})
			if err != nil || len(got) != 1 || got[0].Content != first.Content {
				t.Fatalf("restart lost original: %#v %v", got, err)
			}
			if _, err := reopened.SQL.Exec(reopened.Bind("DELETE FROM instances WHERE id = ?"), "fixture-instance"); err != nil {
				t.Fatal(err)
			}
			got, err = NewStore(reopened, "fixture-instance").Export(ctx)
			if err != nil || len(got) != 0 {
				t.Fatalf("instance cascade: %#v %v", got, err)
			}
			got, err = NewStore(reopened, "fixture-other").Export(ctx)
			if err != nil || len(got) != 1 {
				t.Fatalf("deleted another instance: %#v %v", got, err)
			}
		})
	}
}

func TestSnippetsBoundedOriginals(t *testing.T) {
	r := fixtureRecord("fixture-long", strings.Repeat("原", 5000)+"关键词"+strings.Repeat("文", 5000))
	records := make([]Record, MaxSearchResults)
	for i := range records {
		records[i] = r
	}
	hits := Snippets(records, []string{"关键词"})
	total := 0
	for _, hit := range hits {
		total += len([]rune(hit.Content))
		if !hit.Truncated || len([]rune(hit.Content)) > 1200 || !strings.Contains(r.Content, hit.Content) {
			t.Fatal("snippet is not a bounded exact original")
		}
	}
	if total > 8000 || !strings.Contains(hits[0].Content, "关键词") || len(r.Content) != len(records[0].Content) {
		t.Fatal("snippet budget or original changed")
	}
}

func TestRecorderBoundedQueueAndDrain(t *testing.T) {
	var writes atomic.Int32
	var warnings atomic.Int32
	r := NewRecorder(&Store{}, func(string) string { return "" }, func(string) { warnings.Add(1) })
	r.write = func(context.Context, Record) error { writes.Add(1); return nil }
	for i := range 64 {
		if !r.Record(fixtureRecord(fmt.Sprint(i), "original")) {
			t.Fatal("queue rejected within capacity")
		}
	}
	for range 10 {
		if r.Record(fixtureRecord("overflow", "secret")) {
			t.Fatal("unbounded queue")
		}
	}
	if warnings.Load() != 1 {
		t.Fatal("overflow warnings not rate limited")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Run(ctx)
	if writes.Load() != 64 || r.pending.Load() != 0 || r.Record(fixtureRecord("after-stop", "x")) {
		t.Fatal("shutdown did not drain or stop")
	}
}

func TestRecorderRetryFailureAndDedup(t *testing.T) {
	db, _, _ := historyDB(t, storage.SQLite)
	store := NewStore(db, "fixture-instance")
	var attempts atomic.Int32
	r := NewRecorder(store, func(string) string { return "" }, nil)
	r.write = func(ctx context.Context, rec Record) error {
		if attempts.Add(1) < 3 {
			return errors.New("transient")
		}
		return store.Save(ctx, rec)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	record := fixtureRecord("", "preserved original")
	record.MessageID = "fixture-platform-message"
	r.Record(record)
	r.Record(record)
	flushCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := r.Flush(flushCtx); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	got, err := store.Export(context.Background())
	if err != nil || len(got) != 1 || attempts.Load() != 4 {
		t.Fatalf("retry/dedup: %#v %d %v", got, attempts.Load(), err)
	}

	var failed atomic.Int32
	r = NewRecorder(store, func(string) string { return "" }, nil)
	r.write = func(context.Context, Record) error { failed.Add(1); return errors.New("unavailable") }
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	r.Record(fixtureRecord("fixture-failure", "not committed"))
	if err := r.Flush(flushCtx); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if failed.Load() != 3 {
		t.Fatalf("retry count %d", failed.Load())
	}
}

func TestRecorderConcurrentStop(t *testing.T) {
	r := NewRecorder(&Store{}, func(string) string { return "" }, nil)
	r.write = func(context.Context, Record) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				r.Record(fixtureRecord("fixture-concurrent", "original"))
			}
		}()
	}
	cancel()
	wg.Wait()
	<-done
	if r.pending.Load() != 0 {
		t.Fatalf("shutdown left %d records pending", r.pending.Load())
	}
}

func TestRecorderPolicyChangesRetainArchive(t *testing.T) {
	db, _, _ := historyDB(t, storage.SQLite)
	cfg := map[string]string{GroupWhitelistKey: "fixture-group"}
	r := NewRecorder(NewStore(db, "fixture-instance"), func(k string) string { return cfg[k] }, nil)
	first := fixtureRecord("fixture-group-first", "原文")
	first.Scope.MessageType = "group"
	first.TargetID = "fixture-group"
	if !r.Record(first) {
		t.Fatal("whitelisted group not recorded")
	}
	cfg[GroupWhitelistKey] = ""
	second := first
	second.ID = "fixture-group-second"
	if r.Record(second) {
		t.Fatal("removed group recorded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Run(ctx)
	got, err := r.Store.Search(context.Background(), first.Scope, Query{Keywords: []string{"原文"}})
	if err != nil || len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("old records should remain searchable: %#v %v", got, err)
	}
}
