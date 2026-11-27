package logs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	logspkg "github.com/GuaiZai233/FrostAgent/internal/logs"
)

func (s *Service) query(req *v1.ListLogsRequest, header http.Header) ([]logspkg.LogEntry, *v1.LogCoverage, error) {
	store := s.selectedStore(header)
	if req.Scope != "" {
		switch req.Scope {
		case "control-plane":
			if s.store != logspkg.General {
				return nil, nil, errors.New("control-plane logs require the deployment endpoint")
			}
			store = logspkg.General
		case "instance":
			if s.store == logspkg.General {
				return nil, nil, errors.New("instance logs require an instance endpoint")
			}
			store = s.store
		default:
			return nil, nil, errors.New("invalid log scope")
		}
	}
	if store == nil {
		return nil, &v1.LogCoverage{}, nil
	}
	since, until, err := timeFilters(req.Since, req.Until)
	if err != nil {
		return nil, nil, err
	}
	entries := store.Snapshot()
	coverage := &v1.LogCoverage{Retention: "Capacity bounded; retained until oldest records exceed the byte budget; no age-based expiration."}
	status := store.PersistenceStatus()
	coverage.Persistent = status.Enabled
	coverage.Error = status.Error
	coverage.BudgetBytes = status.BudgetBytes
	var earliest, latest time.Time
	for _, e := range entries {
		if earliest.IsZero() || e.Timestamp.Before(earliest) {
			earliest = e.Timestamp
		}
		if latest.IsZero() || e.Timestamp.After(latest) {
			latest = e.Timestamp
		}
	}
	if !earliest.IsZero() {
		coverage.Earliest = earliest.Format(time.RFC3339Nano)
		coverage.Latest = latest.Format(time.RFC3339Nano)
	}
	filtered := []logspkg.LogEntry{}
	for _, e := range entries {
		if !matchesFilter(e, req.MinLevel, req.SourceFilter) || !extendedFilter(e, since, until, req.TraceId, req.Search) {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, coverage, nil
}
func timeFilters(since, until string) (time.Time, time.Time, error) {
	var a, b time.Time
	var err error
	if since != "" {
		a, err = time.Parse(time.RFC3339Nano, since)
		if err != nil {
			return a, b, errors.New("since must be an RFC3339 timestamp")
		}
	}
	if until != "" {
		b, err = time.Parse(time.RFC3339Nano, until)
		if err != nil {
			return a, b, errors.New("until must be an RFC3339 timestamp")
		}
	}
	if !a.IsZero() && !b.IsZero() && a.After(b) {
		return a, b, errors.New("since is after until")
	}
	return a, b, nil
}
func extendedFilter(e logspkg.LogEntry, since, until time.Time, trace, search string) bool {
	return (since.IsZero() || !e.Timestamp.Before(since)) && (until.IsZero() || !e.Timestamp.After(until)) && (trace == "" || e.TraceID == trace) && (search == "" || strings.Contains(strings.ToLower(e.Content), strings.ToLower(search)))
}
func presentEntry(e logspkg.LogEntry, scoped, include bool) *v1.LogEntry {
	out := convertEntry(e)
	if scoped && !include {
		out.RequestBody = ""
		out.ResponseBody = ""
		if len(out.Summary) > 500 {
			out.Summary = string([]rune(out.Summary)[:min(500, len([]rune(out.Summary)))]) + "…"
		}
	}
	return out
}
func (s *Service) GetLogStats(ctx context.Context, req *connect.Request[v1.ListLogsRequest]) (*connect.Response[v1.LogStatsResponse], error) {
	entries, coverage, err := s.query(req.Msg, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	result := &v1.LogStatsResponse{Total: int64(len(entries)), Levels: map[string]int64{}, Sources: map[string]int64{}, Coverage: coverage}
	for _, e := range entries {
		result.Levels[string(e.Level)]++
		result.Sources[string(e.Category)]++
	}
	return connect.NewResponse(result), nil
}
func parseCursor(raw string) (uint64, error) {
	var id uint64
	if _, err := fmt.Sscanf(raw, "id:%d", &id); err != nil || raw != fmt.Sprintf("id:%d", id) {
		return 0, errors.New("invalid log cursor")
	}
	return id, nil
}
