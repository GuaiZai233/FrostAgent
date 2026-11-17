package chathistory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestListHistoryDatesPaginationAndIsolation(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db, _, _ := historyDB(t, backend)
			ctx := context.Background()
			store := NewStore(db, "fixture-instance")
			start, _ := time.Parse(time.RFC3339, "2026-01-02T00:00:00+08:00")
			for i := range 7 {
				record := fixtureRecord(fmt.Sprintf("fixture-%d", i), "完整原文\n第二行")
				record.OccurredAt = start.Add(time.Duration(i-1) * time.Hour)
				if i == 6 {
					record.OccurredAt = start.AddDate(0, 0, 1)
				}
				// Multiple IDs at the same timestamp must survive pagination.
				if i == 3 {
					record.OccurredAt = start.Add(time.Hour)
				}
				if err := store.Save(ctx, record); err != nil {
					t.Fatal(err)
				}
			}
			for _, scope := range []Scope{{"qq", "private", "other-user"}, {"qq", "group", "fixture-user"}, {"fixture", "private", "fixture-user"}} {
				record := fixtureRecord("foreign-scope-"+scope.Platform+scope.MessageType+scope.TargetID, "foreign")
				record.Scope, record.OccurredAt = scope, start
				if err := store.Save(ctx, record); err != nil {
					t.Fatal(err)
				}
			}
			record := fixtureRecord("foreign-instance", "foreign")
			record.OccurredAt = start
			if err := NewStore(db, "fixture-other").Save(ctx, record); err != nil {
				t.Fatal(err)
			}
			scope := Scope{"onebot", "private", "fixture-user"}
			query := ListQuery{After: start, Before: start.AddDate(0, 0, 1), Limit: 2}
			var ids []string
			for {
				page, err := store.List(ctx, scope, query)
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range page.Records {
					ids = append(ids, record.ID)
					if record.Content != "完整原文\n第二行" {
						t.Fatal("original content changed or foreign scope leaked")
					}
				}
				if len(ids) == 2 {
					// New arrivals ahead of the cursor must not shift later pages.
					newRecord := fixtureRecord("new-arrival", "new")
					newRecord.OccurredAt = start.Add(20 * time.Hour)
					if err := store.Save(ctx, newRecord); err != nil {
						t.Fatal(err)
					}
					for _, bad := range []ListQuery{
						{Cursor: "malformed"},
						{After: start.Add(time.Hour), Before: query.Before, Cursor: page.NextCursor},
						{After: start, Before: start, Cursor: page.NextCursor},
					} {
						if _, err := store.List(ctx, scope, bad); !errors.Is(err, ErrInvalidListQuery) {
							t.Fatalf("invalid filter accepted: %v", err)
						}
					}
					if _, err := store.List(ctx, Scope{"qq", "private", "other-user"}, ListQuery{After: start, Before: query.Before, Cursor: page.NextCursor}); !errors.Is(err, ErrInvalidListQuery) {
						t.Fatal("cross-conversation cursor accepted", err)
					}
				}
				if page.NextCursor == "" {
					break
				}
				query.Cursor = page.NextCursor
			}
			if fmt.Sprint(ids) != "[fixture-5 fixture-4 fixture-3 fixture-2 fixture-1]" {
				t.Fatalf("date boundaries or stable pagination failed: %v", ids)
			}
			page, err := store.List(ctx, scope, ListQuery{})
			if err != nil || len(page.Records) != 8 {
				t.Fatalf("unfiltered history missing: %v %v", page, err)
			}
		})
	}
}

func TestListHistoryBoundsWithoutTruncation(t *testing.T) {
	db, _, _ := historyDB(t, storage.SQLite)
	ctx := context.Background()
	store := NewStore(db, "fixture-instance")
	for i := range 55 {
		record := fixtureRecord(fmt.Sprintf("fixture-%03d", i), strings.Repeat("x", 50000))
		if err := store.Save(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	var total int
	query := ListQuery{Limit: 10000}
	for {
		page, err := store.List(ctx, fixtureRecord("", "").Scope, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Records) > 50 || len(page.Records)*50000 > 2<<20 {
			t.Fatal("page limits exceeded")
		}
		for _, record := range page.Records {
			if len(record.Content) != 50000 {
				t.Fatal("original was truncated")
			}
		}
		total += len(page.Records)
		if page.NextCursor == "" {
			break
		}
		query.Cursor = page.NextCursor
	}
	if total != 55 {
		t.Fatalf("bounded pages lost originals: %d", total)
	}
}
