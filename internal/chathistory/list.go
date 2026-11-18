package chathistory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrInvalidListQuery = errors.New("invalid chat history list query")

type ListQuery struct {
	After, Before time.Time
	Limit         int
	Cursor        string
}

type Page struct {
	Records    []Record
	NextCursor string
}

type listCursor struct {
	Scope
	After, Before int64
	OccurredAt    int64
	ID            string
}

// List returns complete originals, newest first. The stable (time, ID) cursor
// also binds the conversation and date range, so a changed filter starts over.
func (s *Store) List(ctx context.Context, scope Scope, query ListQuery) (Page, error) {
	scope = scope.Canonical()
	invalid := func() (Page, error) { return Page{}, ErrInvalidListQuery }
	if scope.Validate() != nil || (!query.After.IsZero() && !query.Before.IsZero() && !query.After.Before(query.Before)) {
		return invalid()
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 50)
	where := `instance_id = ? AND platform = ? AND message_type = ? AND target_id = ?`
	args := []any{s.instanceID, scope.Platform, scope.MessageType, scope.TargetID}
	filter := listCursor{Scope: scope}
	if !query.After.IsZero() {
		filter.After = query.After.UnixMilli()
		where += ` AND occurred_at >= ?`
		args = append(args, filter.After)
	}
	if !query.Before.IsZero() {
		filter.Before = query.Before.UnixMilli()
		where += ` AND occurred_at < ?`
		args = append(args, filter.Before)
	}
	if query.Cursor != "" {
		if len(query.Cursor) > 8192 {
			return invalid()
		}
		raw, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		var cursor listCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.ID == "" || len(cursor.ID) > 1024 || cursor.Scope != scope || cursor.After != filter.After || cursor.Before != filter.Before {
			return invalid()
		}
		where += ` AND (occurred_at < ? OR (occurred_at = ? AND id < ?))`
		args = append(args, cursor.OccurredAt, cursor.OccurredAt, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT `+recordColumns+` FROM chat_history WHERE `+where+` ORDER BY occurred_at DESC, id DESC LIMIT ?`), args...)
	if err != nil {
		return Page{}, fmt.Errorf("list chat history: %w", err)
	}
	records, err := readRecords(rows)
	if err != nil {
		return Page{}, err
	}
	// Bound the response without truncating a message. A single original is
	// already limited to 1 MiB at ingestion; later messages remain pageable.
	count, size := 0, 0
	for count < min(limit, len(records)) {
		nextSize := size + len(records[count].Content)
		if count > 0 && nextSize > 2<<20 {
			break
		}
		size = nextSize
		count++
	}
	page := Page{Records: records[:count]}
	if count < len(records) {
		last := records[count-1]
		filter.OccurredAt, filter.ID = last.OccurredAt.UnixMilli(), last.ID
		raw, _ := json.Marshal(filter)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}
