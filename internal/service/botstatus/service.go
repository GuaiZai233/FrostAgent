package botstatus

import (
	"FrostAgent/internal/llm"
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "FrostAgent/gen/proto/frostagent/v1"
)

// Service implements frostagent.v1.BotStatusServiceHandler.
type Service struct {
	engine  *llm.Engine
	version string
}

// New creates a new BotStatusService.
func New(engine *llm.Engine, version string) *Service {
	return &Service{engine: engine, version: version}
}

// GetOverview returns bot status overview.
func (s *Service) GetOverview(
	ctx context.Context,
	req *connect.Request[v1.GetOverviewRequest],
) (*connect.Response[v1.GetOverviewResponse], error) {
	uptime := int64(0)
	if !s.engine.StartedAt.IsZero() {
		uptime = int64(time.Since(s.engine.StartedAt).Seconds())
	}

	status := v1.BotStatus_BOT_STATUS_RUNNING
	if s.engine.SessionManager == nil {
		status = v1.BotStatus_BOT_STATUS_INITIALIZING
	}

	activeSessions := int32(0)
	if s.engine.SessionManager != nil {
		activeSessions = int32(s.engine.SessionManager.Count())
	}

	// Build tool list
	var toolInfos []*v1.ToolInfo
	for _, t := range s.engine.ToolRegistry {
		toolInfos = append(toolInfos, &v1.ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
		})
	}

	resp := &v1.GetOverviewResponse{
		BotName:                "FrostAgent",
		Version:                s.version,
		UptimeSeconds:          uptime,
		TotalMessagesProcessed: s.engine.TotalMessagesProcessed,
		ActiveSessions:         activeSessions,
		CurrentModel:           s.engine.ModelName,
		Status:                 status,
		Tools:                  toolInfos,
	}

	return connect.NewResponse(resp), nil
}

// GetSessions returns a paginated list of active sessions.
func (s *Service) GetSessions(
	ctx context.Context,
	req *connect.Request[v1.GetSessionsRequest],
) (*connect.Response[v1.GetSessionsResponse], error) {
	if s.engine.SessionManager == nil {
		return connect.NewResponse(&v1.GetSessionsResponse{}), nil
	}

	pageSize := int(req.Msg.GetPagination().GetPageSize())
	pageToken := req.Msg.GetPagination().GetPageToken()

	// Parse offset from page token (simple int-as-string token)
	offset := 0
	if pageToken != "" {
		fmt.Sscanf(pageToken, "%d", &offset)
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	sessions := s.engine.SessionManager.ListSessions(offset, pageSize)
	total := s.engine.SessionManager.Count()

	var sessionInfos []*v1.SessionInfo
	for _, sess := range sessions {
		platform := derivePlatform(sess.ConversationID)

		msgCount := 0
		sess.Lock()
		msgCount = len(sess.History)
		sess.Unlock()

		sessionInfos = append(sessionInfos, &v1.SessionInfo{
			SessionId:    sess.ConversationID,
			Platform:     platform,
			MessageCount: int32(msgCount),
			CreatedAt:    sess.CreatedAt.Format(time.RFC3339),
			LastActive:   sess.UpdatedAt.Format(time.RFC3339),
		})
	}

	nextOffset := offset + len(sessionInfos)
	nextToken := ""
	if nextOffset < total {
		nextToken = fmt.Sprintf("%d", nextOffset)
	}

	resp := &v1.GetSessionsResponse{
		Sessions: sessionInfos,
		Pagination: &v1.Pagination{
			PageSize:  int32(pageSize),
			PageToken: nextToken,
			Total:     int32(total),
		},
	}

	return connect.NewResponse(resp), nil
}

// derivePlatform infers the platform from the session ID prefix.
func derivePlatform(sessionID string) string {
	if strings.HasPrefix(sessionID, "group_") {
		return "group"
	}
	if strings.HasPrefix(sessionID, "private_") {
		return "private"
	}
	if strings.Contains(sessionID, "_") {
		return strings.SplitN(sessionID, "_", 2)[0]
	}
	return "unknown"
}
