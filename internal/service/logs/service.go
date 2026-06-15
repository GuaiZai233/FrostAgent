package logs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	logspkg "FrostAgent/internal/logs"
	"connectrpc.com/connect"
)

// Service implements frostagent.v1.LogServiceHandler.
type Service struct{}

// New creates a new LogService.
func New() *Service {
	return &Service{}
}

// ListLogs returns paginated log entries with optional filtering.
func (s *Service) ListLogs(
	ctx context.Context,
	req *connect.Request[v1.ListLogsRequest],
) (*connect.Response[v1.ListLogsResponse], error) {
	entries := logspkg.Snapshot()

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
	if offset >= total {
		return connect.NewResponse(&v1.ListLogsResponse{
			Pagination: &v1.Pagination{PageSize: int32(pageSize), Total: int32(total)},
		}), nil
	}

	end := offset + pageSize
	if end > total {
		end = total
	}

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

	subID, ch := logspkg.Subscribe(func(e logspkg.LogEntry) bool {
		return matchesFilter(e, minLevel, sourceFilter)
	})
	defer logspkg.Unsubscribe(subID)

	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-ch:
			if !ok {
				return nil
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
	logspkg.Clear()
	return connect.NewResponse(&v1.ClearLogsResponse{Success: true}), nil
}

// ── helpers ──

// convertEntry maps internal LogEntry → proto LogEntry.
func convertEntry(e logspkg.LogEntry) *v1.LogEntry {
	return &v1.LogEntry{
		Id:        fmt.Sprintf("%d", e.Timestamp.UnixNano()),
		Timestamp: e.Timestamp.Format(time.RFC3339Nano),
		Level:     toProtoLevel(e.Level),
		Source:    string(e.Category),
		Summary:   e.Content,
		HasDetail: strings.TrimSpace(e.Content) != "",
	}
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
	if sourceFilter != "" && string(e.Category) != sourceFilter {
		return false
	}
	return true
}
