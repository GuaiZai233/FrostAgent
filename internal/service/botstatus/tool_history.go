package botstatus

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

// GetToolHistoryEntry loads parameters and original results only when an
// authenticated inspector expands a call. Quarantined records and private
// diagnostics are never returned.
func (s *Service) GetToolHistoryEntry(ctx context.Context, req *connect.Request[v1.GetToolHistoryEntryRequest]) (*connect.Response[v1.GetToolHistoryEntryResponse], error) {
	ref := req.Msg.GetRefId()
	if !toolhistory.ValidRef(ref) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("工具调用记录 ID 无效"))
	}
	if s.engine.ToolHistory == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("工具历史暂不可用"))
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	record, err := s.engine.ToolHistory.ReadForInspection(readCtx, ref)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("当前实例中未找到该工具记录"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("工具参数读取失败"))
	}
	if req.Msg.SessionId != "" {
		scope, ok := chatHistoryScope(req.Msg.SessionId)
		if !ok || record.Scope.Canonical() != scope || s.mockArchiveScope(scope) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("tool call is outside the selected session"))
		}
	}
	visible := record.ForModel()
	available := !record.Restricted && record.State == "complete"
	response := &v1.GetToolHistoryEntryResponse{RefId: record.RefID, Tool: record.Tool,
		ArgumentsAvailable: available, ArgumentsPending: record.State != "complete"}
	if !req.Msg.GetIncludeResult() {
		response.ArgumentsRaw = visible.ArgumentsRaw
		return connect.NewResponse(response), nil
	}
	offset := int(req.Msg.GetResultOffset())
	if offset < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("结果偏移量无效"))
	}
	response.ResultAvailable = available
	if !available {
		return connect.NewResponse(response), nil
	}
	page, total, end, ok := resultPage(visible.ResultRaw, offset, toolHistoryResultPageChars)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("结果偏移量超过记录长度"))
	}
	response.ResultRaw = page
	response.ResultOffset = int32(offset)
	response.ResultTotalChars = int32(total)
	response.ResultHasMore = end < total
	if response.ResultHasMore {
		response.ResultNextOffset = int32(end)
	}
	return connect.NewResponse(response), nil
}

// resultPage returns up to limit characters of text starting at character
// offset, along with the total character count and the offset just past the
// page. Pages are measured in characters, matching read_tool_history, so
// callers can concatenate pages without splitting a UTF-8 sequence. It scans
// the string in place instead of converting the whole result to runes.
func resultPage(text string, offset, limit int) (page string, total, end int, ok bool) {
	total = utf8.RuneCountInString(text)
	if offset > total {
		return "", total, 0, false
	}
	start := 0
	for skipped := 0; skipped < offset; skipped++ {
		_, size := utf8.DecodeRuneInString(text[start:])
		start += size
	}
	stop, taken := start, 0
	for stop < len(text) && taken < limit {
		_, size := utf8.DecodeRuneInString(text[stop:])
		stop += size
		taken++
	}
	return text[start:stop], total, offset + taken, true
}

// toolHistoryResultPageChars matches the read_tool_history page size, so a
// result page is bounded regardless of how large the archived result is.
const toolHistoryResultPageChars = 8000

const (
	sessionToolActivityMaxSources   = 200
	sessionToolActivityPerSource    = 10
	sessionToolActivityMaxSourceLen = 256
)

// GetSessionToolActivity lists archived tool calls per displayed source message
// so the session timeline can show tool cards without storing them in history.
// It returns call metadata only; arguments and results stay behind
// GetToolHistoryEntry.
func (s *Service) GetSessionToolActivity(ctx context.Context, req *connect.Request[v1.GetSessionToolActivityRequest]) (*connect.Response[v1.GetSessionToolActivityResponse], error) {
	invalid := func() (*connect.Response[v1.GetSessionToolActivityResponse], error) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("会话或来源消息参数无效"))
	}
	sessionID := strings.TrimSpace(req.Msg.GetSessionId())
	ids := req.Msg.GetSourceMessageIds()
	if len(sessionID) > 2048 || len(ids) > sessionToolActivityMaxSources {
		return invalid()
	}
	for _, id := range ids {
		if len(id) > sessionToolActivityMaxSourceLen {
			return invalid()
		}
	}
	scope, ok := chatHistoryScope(sessionID)
	if !ok || s.isMockSessionScope(scope) {
		return invalid()
	}
	if s.engine.ToolHistory == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("工具历史暂不可用"))
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	sources, err := s.engine.ToolHistory.CallsBySources(readCtx, scope, ids, sessionToolActivityPerSource)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("工具记录读取失败"))
	}
	response := &v1.GetSessionToolActivityResponse{}
	for _, source := range sources {
		activity := &v1.SessionToolActivity{SourceMessageId: source.SourceMessageID, Total: int32(source.Total)}
		for _, call := range source.Calls {
			activity.Entries = append(activity.Entries, &v1.SessionToolActivityEntry{RefId: call.RefID, Tool: call.Tool})
		}
		response.Activities = append(response.Activities, activity)
	}
	return connect.NewResponse(response), nil
}
