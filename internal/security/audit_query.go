package security

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
)

type AuditQuery struct {
	Instance string
	ID       string
	Limit    int
	Offset   int
}

// Query filters before applying pagination and returns newest records first.
// List retains its chronological order for existing backup and trace callers.
func (s *AuditStore) Query(ctx context.Context, query AuditQuery) ([]AuditEvent, error) {
	if query.Limit < 1 || query.Limit > 1000 || query.Offset < 0 || query.Offset > 10000 {
		return nil, errors.New("invalid audit pagination")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.querySQL(ctx, query)
	}
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return []AuditEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	keep := query.Limit + query.Offset
	events := make([]AuditEvent, 0, keep)
	next := 0
	reader := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		var event AuditEvent
		if json.Unmarshal(line, &event) == nil &&
			(query.Instance == "" || event.Instance == query.Instance) &&
			(query.ID == "" || event.ID == query.ID) {
			if len(events) < keep {
				events = append(events, event)
			} else {
				events[next] = event
			}
			next = (next + 1) % keep
		}
		if err == io.EOF {
			break
		}
	}
	if len(events) == keep {
		events = append(events[next:], events[:next]...)
	}
	slices.Reverse(events)
	if query.Offset >= len(events) {
		return []AuditEvent{}, nil
	}
	return events[query.Offset:], nil
}
