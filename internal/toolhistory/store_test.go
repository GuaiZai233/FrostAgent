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

func TestRecentRefsReturnsNewestCallsOfOneScopeWithSources(t *testing.T) {
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
	save := func(s *Store, sc chathistory.Scope, source string, offset int) string {
		r := Record{Scope: sc, RefID: NewRef(), RunID: "fixture-run", ActorID: "fixture-actor", Tool: "fixture-tool", SourceMessageID: source,
			ArgumentsRaw: `{"secret":"fixture"}`, ResultRaw: "fixture-result", State: "complete", OccurredAt: base.Add(time.Duration(offset) * time.Second)}
		if err := s.Save(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r.RefID
	}
	store := NewStore(db, "fixture-instance")
	var want []RecentRef
	for i, source := range []string{"source-a", "source-a", "source-b", ""} {
		want = append(want, RecentRef{RefID: save(store, scope, source, i), SourceMessageID: source})
	}
	save(store, chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-user"}, "foreign-scope", 10)
	save(NewStore(db, "fixture-other"), scope, "foreign-instance", 11)

	got, err := store.RecentRefs(ctx, scope, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != want[3] || got[1] != want[2] || got[2] != want[1] {
		t.Fatalf("not the newest three calls, newest first: %+v", got)
	}
	if all, err := store.RecentRefs(ctx, scope, 50); err != nil || len(all) != 4 {
		t.Fatalf("scope or instance leaked into recent references: %d %v", len(all), err)
	}
	if none, err := store.RecentRefs(ctx, scope, 0); err != nil || len(none) != 0 {
		t.Fatalf("non-positive limit must return nothing: %d %v", len(none), err)
	}
	if _, err := store.RecentRefs(ctx, chathistory.Scope{}, 3); err == nil {
		t.Fatal("invalid scope must be rejected")
	}
}
