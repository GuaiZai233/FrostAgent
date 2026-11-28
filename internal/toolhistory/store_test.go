package toolhistory

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

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/google/uuid"
)

func TestToolHistoryPersistenceIsolationAndCascade(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fixture-instance", "fixture-other"} {
		if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 3); err != nil {
			t.Fatal(err)
		}
	}
	scope := chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-group"}
	r := Record{Scope: scope, RefID: NewRef(), RunID: "fixture-run", ActorID: "fixture-actor", Tool: "fixture-create", SourceMessageID: "fixture-message",
		ArgumentsRaw: `{"id":7700000000000000001}`, ResultRaw: `{"orderId":7700000000000000001,"orderIdStr":"7700000000000000001"}`,
		ModelResultRaw: "complete", State: "complete", OccurredAt: time.Now().UTC()}
	if err := NewStore(db, "fixture-instance").Save(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewStore(db, "fixture-instance")
	got, err := s.Read(ctx, scope, r.RefID)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("original changed after reopen: %+v %v", got, err)
	}
	for _, other := range []chathistory.Scope{{Platform: "telegram", MessageType: "group", TargetID: scope.TargetID}, {Platform: "qq", MessageType: "group", TargetID: "fixture-foreign"}, {Platform: "qq", MessageType: "private", TargetID: scope.TargetID}} {
		if _, err := s.Read(ctx, other, r.RefID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("scope leak: %v", err)
		}
	}
	if _, err := NewStore(db, "fixture-other").Read(ctx, scope, r.RefID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("instance leak: %v", err)
	}
	refs, err := s.References(ctx, scope, r.SourceMessageID)
	if err != nil || !reflect.DeepEqual(refs, []string{r.RefID}) {
		t.Fatalf("missing message references: %v %v", refs, err)
	}
	foreign := r
	foreign.TargetID = "fixture-foreign"
	foreign.ResultRaw = "foreign result"
	if err := s.Save(ctx, foreign); err == nil {
		t.Fatal("existing reference moved across scopes")
	}
	if got, err := s.Read(ctx, scope, r.RefID); err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("conflicting reference changed original: %+v %v", got, err)
	}
	if _, err := db.SQL.Exec(db.Bind(`DELETE FROM instances WHERE id=?`), "fixture-instance"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Export(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("cascade failed: %v %v", rows, err)
	}
}

func TestToolHistoryReferencesAndQuarantine(t *testing.T) {
	for range 50 {
		if !ValidRef(NewRef()) {
			t.Fatal("reference must have prefix and UUIDv4")
		}
	}
	for _, ref := range []string{"", "550e8400-e29b-41d4-a716-446655440000", Prefix + "550e8400-e29b-11d4-a716-446655440000", Prefix + "550e8400-e29b-41d4-0716-446655440000"} {
		if ValidRef(ref) {
			t.Fatalf("accepted invalid ref %q", ref)
		}
	}
	r := Record{ArgumentsRaw: "unsafe args", ResultRaw: "unsafe raw", MCPResultRaw: "unsafe envelope", Error: "unsafe error", ModelResultRaw: "quarantined", State: "complete", Restricted: true}
	if got := r.ForModel(); got.ResultRaw != "" || got.ArgumentsRaw != "" || got.MCPResultRaw != "" || got.Error != "" || got.ModelResultRaw != "quarantined" {
		t.Fatalf("quarantine bypass: %+v", got)
	}
	r.Restricted = false
	r.State = "executed"
	if r.ForModel().ResultRaw != "" {
		t.Fatal("uninspected results must remain quarantined")
	}
}

func TestRecentBySourceSelectsPerSourceWithoutCrowding(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"fixture-instance", "fixture-other"} {
		if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 3); err != nil {
			t.Fatal(err)
		}
	}
	scope := chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-user"}
	base := time.Now().UTC().Truncate(time.Millisecond)
	offset := 0
	save := func(s *Store, sc chathistory.Scope, source string) string {
		offset++
		r := Record{Scope: sc, RefID: NewRef(), RunID: "fixture-run", ActorID: "fixture-actor", Tool: "fixture-tool", SourceMessageID: source,
			ArgumentsRaw: `{"secret":"fixture"}`, ResultRaw: "fixture-result", State: "complete", OccurredAt: base.Add(time.Duration(offset) * time.Second)}
		if err := s.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r.RefID
	}
	store := NewStore(db, "fixture-instance")
	older := save(store, scope, "source-old")
	middle := []string{save(store, scope, "source-mid"), save(store, scope, "source-mid")}
	var heavy []string
	for i := 0; i < 40; i++ {
		heavy = append(heavy, save(store, scope, "source-heavy"))
	}
	empty := save(store, scope, "")
	save(store, chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-user"}, "foreign-scope")
	save(NewStore(db, "fixture-other"), scope, "foreign-instance")

	got, err := store.RecentBySource(ctx, scope, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].SourceMessageID != "" || got[1].SourceMessageID != "source-heavy" || got[2].SourceMessageID != "source-mid" {
		t.Fatalf("not the newest three sources, newest first: %+v", got)
	}
	if got[0].Total != 1 || len(got[0].Refs) != 1 || got[0].Refs[0] != empty {
		t.Fatalf("a call without a source message forms its own group: %+v", got[0])
	}
	wantHeavy := []string{heavy[39], heavy[38], heavy[37], heavy[36], heavy[35]}
	if got[1].Total != 40 || len(got[1].Refs) != 5 {
		t.Fatalf("heavy source must keep its newest five and report its real total: %+v", got[1])
	}
	for i, ref := range wantHeavy {
		if got[1].Refs[i] != ref {
			t.Fatalf("heavy refs are not newest first: %+v", got[1].Refs)
		}
	}
	if got[2].Total != 2 || len(got[2].Refs) != 2 || got[2].Refs[0] != middle[1] || got[2].Refs[1] != middle[0] {
		t.Fatalf("an earlier source must survive a heavy newer one: %+v", got[2])
	}
	all, err := store.RecentBySource(ctx, scope, 10, 5)
	if err != nil || len(all) != 4 || all[3].SourceMessageID != "source-old" || all[3].Refs[0] != older {
		t.Fatalf("scope or instance leaked into the selection: %+v %v", all, err)
	}
	for _, args := range [][2]int{{0, 5}, {3, 0}, {-1, 5}} {
		if none, err := store.RecentBySource(ctx, scope, args[0], args[1]); err != nil || len(none) != 0 {
			t.Fatalf("non-positive limits must return nothing: %v %d %v", args, len(none), err)
		}
	}
	if _, err := store.RecentBySource(ctx, chathistory.Scope{}, 3, 5); err == nil {
		t.Fatal("invalid scope must be rejected")
	}
}

// callsDB opens an isolated database; the PostgreSQL variant needs the CI service.
func callsDB(t *testing.T, backend storage.Backend) *storage.DB {
	t.Helper()
	ctx, config := context.Background(), storage.Config{Backend: backend}
	if backend == storage.Postgres {
		base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
		if base == "" {
			t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
		}
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		schema := "fa_calls_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		admin, err := sql.Open("pgx", base)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
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
		parsed.RawQuery = query.Encode()
		config.DSN = parsed.String()
	}
	db, err := storage.OpenWithConfig(ctx, t.TempDir(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"fixture-instance", "fixture-other"} {
		if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 3); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestCallsBySourcesIsScopedExactAndChronological(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) { checkCallsBySources(t, callsDB(t, backend)) })
	}
}

func checkCallsBySources(t *testing.T, db *storage.DB) {
	ctx := context.Background()
	scope := chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-group"}
	otherScope := chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-other-group"}
	base := time.Now().UTC().Truncate(time.Millisecond)
	offset := 0
	save := func(s *Store, sc chathistory.Scope, source, tool, result string) string {
		offset++
		r := Record{Scope: sc, RefID: NewRef(), RunID: "fixture-run", ActorID: "fixture-actor", Tool: tool, SourceMessageID: source,
			ResultRaw: result, State: "complete", OccurredAt: base.Add(time.Duration(offset) * time.Second)}
		if err := s.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r.RefID
	}
	store := NewStore(db, "fixture-instance")
	bigResult := strings.Repeat("原文", 200_000)
	var many []SourceCall
	for i := range 5 {
		tool := fmt.Sprintf("fixture-tool-%d", i)
		many = append(many, SourceCall{RefID: save(store, scope, "source-many", tool, bigResult), Tool: tool})
	}
	single := SourceCall{RefID: save(store, scope, "source-single", "fixture-single-tool", ""), Tool: "fixture-single-tool"}
	save(store, scope, "source-unrequested", "fixture-unrequested", "")
	save(store, otherScope, "source-single", "other_scope_tool", "")
	save(NewStore(db, "fixture-other"), scope, "source-single", "other_instance_tool", "")

	got, err := store.CallsBySources(ctx, scope, []string{"source-many", "source-single", "source-single", "source-none", ""}, 3)
	if err != nil {
		t.Fatal(err)
	}
	bySource := map[string]SourceCalls{}
	for _, source := range got {
		bySource[source.SourceMessageID] = source
	}
	if len(got) != 2 || len(bySource) != 2 {
		t.Fatalf("want two sources with calls, got %+v", got)
	}
	heavy := bySource["source-many"]
	if heavy.Total != 5 || !reflect.DeepEqual(heavy.Calls, many[2:]) {
		t.Fatalf("heavy source must keep its exact total and newest calls oldest-first with tool names: %+v", heavy)
	}
	if one := bySource["source-single"]; one.Total != 1 || !reflect.DeepEqual(one.Calls, []SourceCall{single}) {
		t.Fatalf("other scope or instance leaked: %+v", one)
	}
	if none, err := store.CallsBySources(ctx, scope, []string{"source-many"}, 0); err != nil || len(none) != 0 {
		t.Fatalf("non-positive limit must return nothing: %+v %v", none, err)
	}
	if none, err := store.CallsBySources(ctx, scope, []string{"", ""}, 3); err != nil || len(none) != 0 {
		t.Fatalf("no usable source must return nothing: %+v %v", none, err)
	}
	if _, err := store.CallsBySources(ctx, chathistory.Scope{}, []string{"source-many"}, 3); err == nil {
		t.Fatal("invalid scope accepted")
	}
}
