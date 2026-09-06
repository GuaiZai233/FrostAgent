package logs

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	logspkg "FrostAgent/internal/logs"
	"connectrpc.com/connect"
)

// Service implements frostagent.v1.LogServiceHandler.
type Service struct{ store *logspkg.Store }

// New creates a new LogService.
func New(stores ...*logspkg.Store) *Service {
	store := logspkg.General
	if len(stores) > 0 {
		store = stores[0]
	}
	return &Service{store: store}
}

// ListLogs returns paginated log entries with optional filtering.
func (s *Service) ListLogs(
	ctx context.Context,
	req *connect.Request[v1.ListLogsRequest],
) (*connect.Response[v1.ListLogsResponse], error) {
	entries := s.store.Snapshot()
	if s.store == logspkg.General && req.Header().Get("X-FrostAgent-General") == "false" {
		entries = nil
	}
	if s.store != logspkg.General && req.Header().Get("X-FrostAgent-General") == "true" {
		entries = append(entries, logspkg.General.Snapshot()...)
	}

	// Filter
	minLevel := req.Msg.GetMinLevel()
	sourceFilter := req.Msg.GetSourceFilter()
	filtered := make([]logspkg.LogEntry, 0, len(entries))
	for _, e := range entries {
		if !matchesFilter(e, minLevel, sourceFilter) {
			continue
		}
		filtered = append(filtered, e)
	}

	// Sort by timestamp descending
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	// Pagination
	pageSize := int(req.Msg.GetPagination().GetPageSize())
	pageToken := req.Msg.GetPagination().GetPageToken()
	offset := 0
	if pageToken != "" {
		fmt.Sscanf(pageToken, "%d", &offset)
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	total := len(filtered)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return connect.NewResponse(&v1.ListLogsResponse{
			Pagination: &v1.Pagination{PageSize: int32(pageSize), Total: int32(total)},
		}), nil
	}

	end := min(offset+pageSize, total)

	var pbEntries []*v1.LogEntry
	for i := offset; i < end; i++ {
		pbEntries = append(pbEntries, convertEntry(filtered[i]))
	}

	nextToken := ""
	if end < total {
		nextToken = fmt.Sprintf("%d", end)
	}

	resp := &v1.ListLogsResponse{
		Entries: pbEntries,
		Pagination: &v1.Pagination{
			PageSize:  int32(pageSize),
			PageToken: nextToken,
			Total:     int32(total),
		},
	}
	return connect.NewResponse(resp), nil
}

// StreamLogs streams new log entries to the client via server-sent events.
func (s *Service) StreamLogs(
	ctx context.Context,
	req *connect.Request[v1.StreamLogsRequest],
	stream *connect.ServerStream[v1.LogEntry],
) error {
	minLevel := req.Msg.GetMinLevel()
	sourceFilter := req.Msg.GetSourceFilter()
	if s.store == logspkg.General && req.Header().Get("X-FrostAgent-General") == "false" {
		<-ctx.Done()
		return nil
	}

	subID, ch := s.store.Subscribe(func(e logspkg.LogEntry) bool {
		return matchesFilter(e, minLevel, sourceFilter)
	})
	defer s.store.Unsubscribe(subID)

	var general <-chan logspkg.LogEntry
	if s.store != logspkg.General && req.Header().Get("X-FrostAgent-General") == "true" {
		id, c := logspkg.General.Subscribe(func(e logspkg.LogEntry) bool { return matchesFilter(e, minLevel, sourceFilter) })
		general = c
		defer logspkg.General.Unsubscribe(id)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-general:
			if !ok {
				return nil
			}
			if err := stream.Send(convertEntry(entry)); err != nil {
				return err
			}
		case entry, ok := <-ch:
			if !ok {
				if general == nil {
					return nil
				}
				ch = nil
				continue
			}
			if err := stream.Send(convertEntry(entry)); err != nil {
				return err
			}
		}
	}
}

// ClearLogs clears the in-memory log buffer.
func (s *Service) ClearLogs(
	ctx context.Context,
	req *connect.Request[v1.ClearLogsRequest],
) (*connect.Response[v1.ClearLogsResponse], error) {
	if s.store != logspkg.General || req.Header().Get("X-FrostAgent-General") != "false" {
		s.store.Clear()
	}
	if s.store != logspkg.General && req.Header().Get("X-FrostAgent-General") == "true" {
		logspkg.General.Clear()
	}
	return connect.NewResponse(&v1.ClearLogsResponse{Success: true}), nil
}

// ── helpers ──

// convertEntry maps internal LogEntry → proto LogEntry.
func convertEntry(e logspkg.LogEntry) *v1.LogEntry {
	entry := &v1.LogEntry{
		Id:           e.InstanceID + ":" + strconv.FormatUint(e.ID, 10),
		InstanceId:   e.InstanceID,
		InstanceName: e.InstanceName,
		Timestamp:    e.Timestamp.Format(time.RFC3339Nano),
		Level:        toProtoLevel(e.Level),
		Source:       string(e.Category),
		Summary:      e.Content,
		HasDetail:    strings.TrimSpace(e.Content) != "",
	}
	switch e.Category {
	case logspkg.LLM_REQUEST:
		entry.RequestBody = e.Content
	case logspkg.LLM_RESPONSE:
		entry.ResponseBody = e.Content
	}
	return entry
}

// levelPasses checks whether an internal Level meets the minimum proto LogLevel.
func levelPasses(lvl logspkg.Level, min v1.LogLevel) bool {
	return toProtoLevel(lvl) >= min
}

// toProtoLevel converts internal Level string to proto LogLevel.
func toProtoLevel(lvl logspkg.Level) v1.LogLevel {
	switch lvl {
	case logspkg.DEBUG:
		return v1.LogLevel_LOG_LEVEL_DEBUG
	case logspkg.INFO:
		return v1.LogLevel_LOG_LEVEL_INFO
	case logspkg.WARN:
		return v1.LogLevel_LOG_LEVEL_WARN
	case logspkg.ERROR:
		return v1.LogLevel_LOG_LEVEL_ERROR
	default:
		return v1.LogLevel_LOG_LEVEL_UNSPECIFIED
	}
}

func matchesFilter(e logspkg.LogEntry, minLevel v1.LogLevel, sourceFilter string) bool {
	// 1. 等级过滤
	if !levelPasses(e.Level, minLevel) {
		return false
	}
	// 2. 来源/分类过滤
	if sourceFilter != "" {
		sf := strings.ToLower(sourceFilter)
		cat := strings.ToLower(string(e.Category))
		if sf == "llm" {
			if !strings.HasPrefix(cat, "llm") {
				return false
			}
		} else if cat != sf && string(e.Category) != sourceFilter {
			return false
		}
	}
	return true
}
