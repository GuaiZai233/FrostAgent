package security

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestAuditQueryFiltersBeforePaginationAndPreservesList(t *testing.T) {
	for _, kind := range []string{"file", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			store := NewAuditStore(filepath.Join(t.TempDir(), "audit.jsonl"), 1000)
			if kind == "sqlite" {
				db, err := storage.Open(context.Background(), t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				store = NewSQLAuditStore(db, 1000)
			}
			for n, id := range []string{"a", "b", "c", "d", "e"} {
				instance := "synthetic-target"
				if n%2 == 1 {
					instance = "synthetic-other"
				}
				if err := store.Append(AuditEvent{ID: id, At: time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC), Instance: instance}); err != nil {
					t.Fatal(err)
				}
			}
			ids := func(events []AuditEvent, err error) []string {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				out := []string{}
				for _, event := range events {
					out = append(out, event.ID)
				}
				return out
			}
			if got := ids(store.List(2)); !reflect.DeepEqual(got, []string{"d", "e"}) {
				t.Fatalf("List order changed: %v", got)
			}
			for _, tc := range []struct {
				query AuditQuery
				want  []string
			}{
				{AuditQuery{Instance: "synthetic-target", Limit: 2}, []string{"e", "c"}},
				{AuditQuery{Instance: "synthetic-target", Limit: 2, Offset: 2}, []string{"a"}},
				{AuditQuery{Instance: "synthetic-target", ID: "a", Limit: 1}, []string{"a"}},
				{AuditQuery{Instance: "synthetic-other", ID: "a", Limit: 1}, []string{}},
				{AuditQuery{Limit: 2, Offset: 5}, []string{}},
			} {
				if got := ids(store.Query(context.Background(), tc.query)); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("query %+v: %v, want %v", tc.query, got, tc.want)
				}
			}
			if store.db != nil {
				// A malformed excluded row proves filtering happens before decoding.
				if _, err := store.db.SQL.Exec(`UPDATE security_audit SET occurred_at='invalid' WHERE id='d'`); err != nil {
					t.Fatal(err)
				}
				if got := ids(store.Query(context.Background(), AuditQuery{Instance: "synthetic-target", Limit: 1})); !reflect.DeepEqual(got, []string{"e"}) {
					t.Fatal(got)
				}
			}
		})
	}
}
