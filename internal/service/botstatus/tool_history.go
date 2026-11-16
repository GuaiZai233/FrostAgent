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

// GetToolHistoryEntry loads parameters only when an authenticated inspector
// expands a call. Original results and private diagnostics are never returned.
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
	return connect.NewResponse(&v1.GetToolHistoryEntryResponse{RefId: record.RefID, Tool: record.Tool,
		ArgumentsRaw: visible.ArgumentsRaw, ArgumentsAvailable: !record.Restricted && record.State == "complete"}), nil
}
