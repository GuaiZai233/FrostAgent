package botstatus

import (
	"context"
	"database/sql"
	"errors"
	"time"

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
	visible := record.ForModel()
	available := !record.Restricted && record.State == "complete"
	response := &v1.GetToolHistoryEntryResponse{RefId: record.RefID, Tool: record.Tool,
		ArgumentsRaw: visible.ArgumentsRaw, ArgumentsAvailable: available,
		ArgumentsPending: record.State != "complete"}
	if !req.Msg.GetIncludeResult() {
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
	// Pages are measured in characters, matching read_tool_history, so
	// callers can concatenate pages without splitting a UTF-8 sequence.
	text := []rune(visible.ResultRaw)
	if offset > len(text) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("结果偏移量超过记录长度"))
	}
	end := min(len(text), offset+toolHistoryResultPageChars)
	response.ResultRaw = string(text[offset:end])
	response.ResultOffset = int32(offset)
	response.ResultTotalChars = int32(len(text))
	response.ResultHasMore = end < len(text)
	if response.ResultHasMore {
		response.ResultNextOffset = int32(end)
	}
	return connect.NewResponse(response), nil
}

// toolHistoryResultPageChars matches the read_tool_history page size, so a
// result page is bounded regardless of how large the archived result is.
const toolHistoryResultPageChars = 8000
