package botstatus

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"

	"connectrpc.com/connect"
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

	// Sort by name to ensure deterministic order, with a stable tiebreaker for robustness.
	sort.SliceStable(toolInfos, func(i, j int) bool {
		if toolInfos[i] == nil {
			return false
		}
		if toolInfos[j] == nil {
			return true
		}
		if toolInfos[i].Name == toolInfos[j].Name {
			return toolInfos[i].Description < toolInfos[j].Description
		}
		return toolInfos[i].Name < toolInfos[j].Name
	})

	resp := &v1.GetOverviewResponse{
		BotName:                "FrostAgent",
		Version:                s.version,
		UptimeSeconds:          uptime,
		TotalMessagesProcessed: s.engine.TotalMessagesProcessed.Load(),
		ActiveSessions:         activeSessions,
		CurrentModel:           s.engine.ModelName,
		Status:                 status,
		Tools:                  toolInfos,
	}

	return connect.NewResponse(resp), nil
}

// GetSessions merges active sessions with durable group summaries before
// sorting and pagination, so every group appears at most once.
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
	if offset < 0 {
		offset = 0
	}

	type sessionView struct {
		info      *v1.SessionInfo
		createdAt time.Time
		updatedAt time.Time
	}
	viewsByID := make(map[string]sessionView)
	sessions := s.engine.SessionManager.ListSessions(0, 0)
	for _, sess := range sessions {
		platform := derivePlatform(sess.ConversationID)

		sess.Lock()
		msgCount := len(sess.History)
		createdAt := sess.CreatedAt
		updatedAt := sess.UpdatedAt
		sess.Unlock()
		viewsByID[sess.ConversationID] = sessionView{
			info: &v1.SessionInfo{
				SessionId:    sess.ConversationID,
				Platform:     platform,
				MessageCount: int32(msgCount),
				CreatedAt:    createdAt.Format(time.RFC3339),
				LastActive:   updatedAt.Format(time.RFC3339),
				GroupSummary: sess.GroupRunningSummary(),
			},
			createdAt: createdAt,
			updatedAt: updatedAt,
		}
	}

	if s.engine.GroupSummaryStore != nil {
		records, err := s.engine.GroupSummaryStore.List()
		if err != nil {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("读取持久化群聊总结失败: %v", err))
		} else {
			for _, record := range records {
				if existing, ok := viewsByID[record.SessionID]; ok {
					if existing.info.GroupSummary == "" {
						existing.info.GroupSummary = record.Summary
					}
					if record.CreatedAt.Before(existing.createdAt) {
						existing.createdAt = record.CreatedAt
						existing.info.CreatedAt = record.CreatedAt.Format(time.RFC3339)
					}
					if record.UpdatedAt.After(existing.updatedAt) {
						existing.updatedAt = record.UpdatedAt
						existing.info.LastActive = record.UpdatedAt.Format(time.RFC3339)
					}
					viewsByID[record.SessionID] = existing
					continue
				}
				viewsByID[record.SessionID] = sessionView{
					info: &v1.SessionInfo{
						SessionId:    record.SessionID,
						Platform:     derivePlatform(record.SessionID),
						CreatedAt:    record.CreatedAt.Format(time.RFC3339),
						LastActive:   record.UpdatedAt.Format(time.RFC3339),
						GroupSummary: record.Summary,
					},
					createdAt: record.CreatedAt,
					updatedAt: record.UpdatedAt,
				}
			}
		}
	}

	views := make([]sessionView, 0, len(viewsByID))
	for _, view := range viewsByID {
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].updatedAt.Equal(views[j].updatedAt) {
			return views[i].info.SessionId < views[j].info.SessionId
		}
		return views[i].updatedAt.After(views[j].updatedAt)
	})

	total := len(views)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	sessionInfos := make([]*v1.SessionInfo, 0, end-offset)
	for _, view := range views[offset:end] {
		sessionInfos = append(sessionInfos, view.info)
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

// DeleteGroupSummary resets active compact state and removes its durable copy.
func (s *Service) DeleteGroupSummary(
	ctx context.Context,
	req *connect.Request[v1.DeleteGroupSummaryRequest],
) (*connect.Response[v1.DeleteGroupSummaryResponse], error) {
	sessionID := strings.TrimSpace(req.Msg.GetSessionId())
	if sessionID == "" || !isGroupSession(sessionID) {
		return connect.NewResponse(&v1.DeleteGroupSummaryResponse{
			Error: "a group session_id is required",
		}), nil
	}
	if s.engine.GroupSummaryStore == nil {
		return connect.NewResponse(&v1.DeleteGroupSummaryResponse{
			Error: "group summary store is unavailable",
		}), nil
	}

	if s.engine.SessionManager != nil {
		s.engine.SessionManager.ResetGroupCompact(sessionID)
	}
	if err := s.engine.GroupSummaryStore.Delete(sessionID); err != nil {
		return connect.NewResponse(&v1.DeleteGroupSummaryResponse{
			Error: err.Error(),
		}), nil
	}
	logs.Info(logs.SYSTEM, "群聊总结已删除 ("+sessionID+")")
	return connect.NewResponse(&v1.DeleteGroupSummaryResponse{Success: true}), nil
}

func isGroupSession(sessionID string) bool {
	return strings.HasPrefix(sessionID, "group:") || strings.Contains(sessionID, ":group:")
}

// derivePlatform infers the platform from the session ID prefix.
func derivePlatform(sessionID string) string {
	if strings.HasPrefix(sessionID, "astrbot:") {
		return "astrbot"
	}
	if strings.HasPrefix(sessionID, "onebot:") || strings.HasPrefix(sessionID, "qq:") {
		return "onebot"
	}
	if platform, _, ok := strings.Cut(sessionID, ":"); ok {
		return platform
	}
	if strings.Contains(sessionID, "_") {
		return strings.SplitN(sessionID, "_", 2)[0]
	}
	return "unknown"
}
