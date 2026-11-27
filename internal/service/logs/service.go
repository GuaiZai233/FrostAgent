package logs

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	logspkg "github.com/GuaiZai233/FrostAgent/internal/logs"
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
	query := req.Msg
	if req.Msg.ErrorUuid != "" {
		query = &v1.ListLogsRequest{Scope: req.Msg.Scope}
	}
	entries, coverage, err := s.query(query, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if req.Msg.ErrorUuid != "" {
		id, ok := logspkg.NormalizeErrorUUID(req.Msg.ErrorUuid)
		if !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid Error UUID"))
		}
		for _, e := range entries {
			if e.ErrorUUID == id {
				return connect.NewResponse(&v1.ListLogsResponse{Entries: []*v1.LogEntry{presentEntry(e, req.Msg.Scope != "", req.Msg.IncludePayload)}, Coverage: coverage, Pagination: &v1.Pagination{Total: 1}}), nil
			}
		}
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("error is outside retained log coverage"))
	}
	filtered := entries

	// Sort by timestamp descending
	sort.Slice(filtered, func(i, j int) bool {
		if req.Msg.Scope != "" {
			return filtered[i].ID > filtered[j].ID
		}
		if filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})

	pageSize := int(req.Msg.GetPagination().GetPageSize())
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 10000 {
		pageSize = 10000
	}
	pageToken := req.Msg.GetPagination().GetPageToken()
	offset := 0
	total := len(filtered)
	if req.Msg.Scope != "" && pageToken != "" {
		before, err := parseCursor(pageToken)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		remaining := filtered[:0]
		for _, e := range filtered {
			if e.ID < before {
				remaining = append(remaining, e)
			}
		}
		filtered = remaining
	} else if pageToken != "" {
		var err error
		offset, err = strconv.Atoi(pageToken)
		if err != nil || offset < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page token"))
		}
	}
	offset = min(offset, len(filtered))
	end := min(offset+pageSize, len(filtered))
	pbEntries := []*v1.LogEntry{}
	for _, e := range filtered[offset:end] {
		pbEntries = append(pbEntries, presentEntry(e, req.Msg.Scope != "", req.Msg.IncludePayload))
	}
	next := ""
	if end < len(filtered) {
		if req.Msg.Scope != "" {
			next = fmt.Sprintf("id:%d", filtered[end-1].ID)
		} else {
			next = strconv.Itoa(end)
		}
	}
	resp := &v1.ListLogsResponse{Entries: pbEntries, Coverage: coverage, Pagination: &v1.Pagination{PageSize: int32(pageSize), PageToken: next, Total: int32(total)}}
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
	since, until, err := timeFilters(req.Msg.Since, req.Msg.Until)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	store := s.selectedStore(req.Header())
	if req.Msg.Scope != "" {
		if _, _, err := s.query(&v1.ListLogsRequest{Scope: req.Msg.Scope}, req.Header()); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		store = s.store
	}
	if store == nil {
		<-ctx.Done()
		return nil
	}

	subID, ch := store.Subscribe(func(e logspkg.LogEntry) bool {
		return matchesFilter(e, minLevel, sourceFilter) && extendedFilter(e, since, until, req.Msg.TraceId, req.Msg.Search)
	})
	defer store.Unsubscribe(subID)

	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(presentEntry(entry, req.Msg.Scope != "", false)); err != nil {
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
	store := s.selectedStore(req.Header())
	if req.Msg.Scope != "" {
		if _, _, err := s.query(&v1.ListLogsRequest{Scope: req.Msg.Scope}, req.Header()); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		store = s.store
	}
	if store != nil {
		store.Clear()
		if status := store.PersistenceStatus(); status.Error != "" {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("%s", status.Error))
		}
	}
	return connect.NewResponse(&v1.ClearLogsResponse{Success: true}), nil
}

func (s *Service) selectedStore(header http.Header) *logspkg.Store {
	source := strings.TrimSpace(header.Get("X-FrostAgent-Log-Source"))
	if source == "control-plane" || (source == "" && header.Get("X-FrostAgent-General") == "true") {
		return logspkg.General
	}
	if source != "" && source != "instance" {
		return nil
	}
	if s.store == logspkg.General {
		return nil
	}
	return s.store
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
		TraceId:      e.TraceID,
		ErrorUuid:    e.ErrorUUID,
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
