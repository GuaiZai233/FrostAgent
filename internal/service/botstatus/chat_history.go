package botstatus

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
)

// GetSessionHistory uses the authenticated, instance-bound service's archive.
// It never changes collection policy or restores originals into model context.
func (s *Service) GetSessionHistory(ctx context.Context, req *connect.Request[v1.GetSessionHistoryRequest]) (*connect.Response[v1.GetSessionHistoryResponse], error) {
	invalid := func() (*connect.Response[v1.GetSessionHistoryResponse], error) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("会话、日期范围或分页参数无效"))
	}
	sessionID := strings.TrimSpace(req.Msg.GetSessionId())
	if len(sessionID) > 2048 || req.Msg.GetPageSize() < 0 {
		return invalid()
	}
	scope, ok := chatHistoryScope(sessionID)
	if !ok {
		return invalid()
	}
	if s.isMockSessionScope(scope) {
		return invalid()
	}
	query := chathistory.ListQuery{Limit: int(req.Msg.GetPageSize()), Cursor: req.Msg.GetPageToken()}
	for _, field := range []struct {
		raw string
		dst *time.Time
	}{{req.Msg.GetAfter(), &query.After}, {req.Msg.GetBefore(), &query.Before}} {
		if field.raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, field.raw)
		if err != nil {
			return invalid()
		}
		*field.dst = parsed
	}
	if !query.After.IsZero() && !query.Before.IsZero() && !query.After.Before(query.Before) {
		return invalid()
	}
	if s.engine.ChatHistory == nil || s.engine.ChatHistory.Store == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("聊天历史暂不可用"))
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	page, err := s.engine.ChatHistory.Store.List(readCtx, scope, query)
	if errors.Is(err, chathistory.ErrInvalidListQuery) {
		return invalid()
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("聊天历史读取失败，请稍后重试"))
	}
	coverage, err := s.engine.ChatHistory.Store.Coverage(readCtx, scope)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("archive coverage unavailable"))
	}
	response := &v1.GetSessionHistoryResponse{NextPageToken: page.NextCursor, Coverage: archiveCoverage(coverage)}
	for _, record := range page.Records {
		response.Records = append(response.Records, &v1.SessionChatRecord{
			Id: record.ID, MessageId: record.MessageID, SenderId: record.SenderID,
			SenderName: record.SenderName, Role: record.Role, Content: record.Content,
			OccurredAt: record.OccurredAt.Format(time.RFC3339Nano), ReplyTo: record.ReplyTo,
			DeliveryStatus: record.DeliveryStatus,
		})
	}
	return connect.NewResponse(response), nil
}

func chatHistoryScope(sessionID string) (chathistory.Scope, bool) {
	canonical := memory.CanonicalSessionKey(sessionID)
	parts := strings.SplitN(canonical, ":", 3)
	var scope chathistory.Scope
	if len(parts) >= 2 && (parts[0] == "group" || parts[0] == "private") {
		// Preserve colons inside opaque native QQ target IDs as well.
		_, target, _ := strings.Cut(canonical, ":")
		scope = chathistory.Scope{Platform: "qq", MessageType: parts[0], TargetID: target}
	} else if len(parts) == 3 {
		scope = chathistory.Scope{Platform: parts[0], MessageType: parts[1], TargetID: parts[2]}
	}
	scope = scope.Canonical()
	return scope, scope.Validate() == nil
}

// isMockSessionScope reports whether a scope belongs to a simulated session.
// Compare the same canonical archive identities on both sides: raw session keys
// may differ in platform casing, aliases or whitespace.
func (s *Service) isMockSessionScope(scope chathistory.Scope) bool {
	if s.engine.SessionManager == nil {
		return false
	}
	for _, id := range s.engine.SessionManager.MockSessionIDs() {
		if mockScope, ok := chatHistoryScope(id); ok && mockScope == scope {
			return true
		}
	}
	return false
}
