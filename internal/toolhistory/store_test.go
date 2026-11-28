package toolhistory

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
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

func TestRefsBySourcesIsScopedExactAndChronological(t *testing.T) {
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
	scope := chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-group"}
	otherScope := chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-other-group"}
	base := time.Now().UTC().Truncate(time.Millisecond)
	offset := 0
	save := func(s *Store, sc chathistory.Scope, source string) string {
		offset++
		r := Record{Scope: sc, RefID: NewRef(), RunID: "fixture-run", ActorID: "fixture-actor", Tool: "fixture-tool", SourceMessageID: source,
			State: "complete", OccurredAt: base.Add(time.Duration(offset) * time.Second)}
		if err := s.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r.RefID
	}
	store := NewStore(db, "fixture-instance")
	var many []string
	for range 5 {
		many = append(many, save(store, scope, "source-many"))
	}
	single := save(store, scope, "source-single")
	save(store, scope, "source-unrequested")
	save(store, otherScope, "source-single")
	save(NewStore(db, "fixture-other"), scope, "source-single")

	got, err := store.RefsBySources(ctx, scope, []string{"source-many", "source-single", "source-single", "source-none", ""}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want two sources with calls, got %+v", got)
	}
	if got[0].SourceMessageID != "source-many" || got[0].Total != 5 || !reflect.DeepEqual(got[0].Refs, many[2:]) {
		t.Fatalf("heavy source must keep exact total and its newest calls oldest-first: %+v", got[0])
	}
	if got[1].SourceMessageID != "source-single" || got[1].Total != 1 || !reflect.DeepEqual(got[1].Refs, []string{single}) {
		t.Fatalf("other scope or instance leaked: %+v", got[1])
	}
	if none, err := store.RefsBySources(ctx, scope, []string{"source-many"}, 0); err != nil || len(none) != 0 {
		t.Fatalf("non-positive limit must return nothing: %+v %v", none, err)
	}
	if _, err := store.RefsBySources(ctx, chathistory.Scope{}, []string{"source-many"}, 3); err == nil {
		t.Fatal("invalid scope accepted")
	}
}
