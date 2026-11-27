package botstatus

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"time"
)

func archiveCoverage(c chathistory.Coverage) *v1.ArchiveCoverage {
	r := &v1.ArchiveCoverage{Count: c.Count, Status: "empty"}
	if c.Count > 0 {
		r.Status = "available"
		r.Since = c.Since.Format(time.RFC3339Nano)
		r.Until = c.Until.Format(time.RFC3339Nano)
	}
	return r
}
func (s *Service) ListSessionTools(ctx context.Context, req *connect.Request[v1.ListSessionToolsRequest]) (*connect.Response[v1.ListSessionToolsResponse], error) {
	scope, ok := chatHistoryScope(req.Msg.SessionId)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session"))
	}
	if s.engine.ToolHistory == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("tool archive unavailable"))
	}
	if s.mockArchiveScope(scope) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mock session has no archive"))
	}
	records, coverage, err := s.engine.ToolHistory.ListForInspection(ctx, scope, int(req.Msg.Limit))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("tool archive read failed"))
	}
	response := &v1.ListSessionToolsResponse{Coverage: archiveCoverage(coverage)}
	for _, record := range records {
		summary, _, _, _ := resultPage(record.ModelResultRaw, 0, 240)
		response.Calls = append(response.Calls, &v1.SessionToolRecord{RefId: record.RefID, RunId: record.RunID, Tool: record.Tool, State: record.State, ResultSummary: summary, OccurredAt: record.OccurredAt.Format(time.RFC3339Nano), Restricted: record.Restricted})
	}
	return connect.NewResponse(response), nil
}

func (s *Service) mockArchiveScope(scope chathistory.Scope) bool {
	if s.engine.SessionManager != nil {
		for _, id := range s.engine.SessionManager.MockSessionIDs() {
			if mock, valid := chatHistoryScope(id); valid && mock == scope {
				return true
			}
		}
	}
	return false
}
