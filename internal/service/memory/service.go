package memsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"FrostAgent/internal/memory"

	"connectrpc.com/connect"
)

// Service implements frostagent.v1.MemoryServiceHandler.
type Service struct {
	store        *memory.Store
	groupManager *memory.GroupManager
	reflections  *memory.ReflectionManager
}

// New creates a new MemoryService.
func New(store *memory.Store, groupManager *memory.GroupManager, reflections *memory.ReflectionManager) *Service {
	return &Service{
		store:        store,
		groupManager: groupManager,
		reflections:  reflections,
	}
}

// ListMemories returns a paginated list of memories, optionally filtered by owner and scope.
func (s *Service) ListMemories(
	ctx context.Context,
	req *connect.Request[v1.ListMemoriesRequest],
) (*connect.Response[v1.ListMemoriesResponse], error) {
	scope := req.Msg.GetScope()
	owner := req.Msg.GetOwner()

	var entries []memory.MemoryEntry
	var err error

	if scope == "group" {
		groupID := req.Msg.GetGroupId()
		if groupID == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("group_id is required for group scope"))
		}
		if s.groupManager == nil {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("group memory manager not initialized"))
		}
		groupStore, gErr := s.groupManager.GetGroupStore(groupID)
		if gErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get group store: %w", gErr))
		}
		if owner == "" {
			entries, err = groupStore.ListAll()
		} else {
			entries, err = groupStore.ListByOwner(owner)
		}
	} else {
		if owner == "" {
			entries, err = s.store.ListAll()
		} else {
			entries, err = s.store.ListByOwner(owner)
		}
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list memories: %w", err))
	}

	resp, err := paginateEntries(entries, req.Msg.GetPagination())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(resp), nil
}

// SearchMemories performs keyword search across memories within the given scope.
func (s *Service) SearchMemories(
	ctx context.Context,
	req *connect.Request[v1.SearchMemoriesRequest],
) (*connect.Response[v1.SearchMemoriesResponse], error) {
	query := req.Msg.GetQuery()
	if query == "" {
		return connect.NewResponse(&v1.SearchMemoriesResponse{}), nil
	}

	scope := req.Msg.GetScope()
	var entries []memory.MemoryEntry
	var err error

	if scope == "group" {
		groupID := req.Msg.GetGroupId()
		if groupID == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("group_id is required for group scope"))
		}
		if s.groupManager == nil {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("group memory manager not initialized"))
		}
		groupStore, gErr := s.groupManager.GetGroupStore(groupID)
		if gErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get group store: %w", gErr))
		}
		entries, err = groupStore.Search(query, 0)
	} else {
		entries, err = s.store.Search(query, 0)
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("search memories: %w", err))
	}

	resp, err := paginateEntries(entries, req.Msg.GetPagination())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&v1.SearchMemoriesResponse{
		Memories:   resp.Memories,
		Pagination: resp.Pagination,
	}), nil
}

// AddMemory manually creates a new memory entry.
func (s *Service) AddMemory(
	ctx context.Context,
	req *connect.Request[v1.AddMemoryRequest],
) (*connect.Response[v1.AddMemoryResponse], error) {
	content := req.Msg.GetContent()
	if content == "" {
		return connect.NewResponse(&v1.AddMemoryResponse{
			Error: "content is required",
		}), nil
	}

	scope := req.Msg.GetScope()
	now := time.Now()

	if scope == "group" {
		groupID := req.Msg.GetGroupId()
		if groupID == "" {
			return connect.NewResponse(&v1.AddMemoryResponse{Error: "group_id is required"}), nil
		}
		if s.groupManager == nil {
			return connect.NewResponse(&v1.AddMemoryResponse{Error: "group memory manager not initialized"}), nil
		}
		groupStore, err := s.groupManager.GetGroupStore(groupID)
		if err != nil {
			return connect.NewResponse(&v1.AddMemoryResponse{Error: err.Error()}), nil
		}
		owner := req.Msg.GetOwner()
		if owner == "" {
			owner = memory.GroupOwnerExplicit
		}
		entry := memory.MemoryEntry{
			ID:        fmt.Sprintf("mem_%d", now.UnixNano()),
			Owner:     owner,
			ScopeType: memory.ScopeGroup,
			GroupID:   groupID,
			Content:   content,
			Tags:      req.Msg.GetTags(),
			Source:    memory.SourceManual,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := groupStore.Save(entry); err != nil {
			return connect.NewResponse(&v1.AddMemoryResponse{Error: fmt.Sprintf("save failed: %v", err)}), nil
		}
		return connect.NewResponse(&v1.AddMemoryResponse{Memory: toProtoEntry(entry)}), nil
	}

	owner := req.Msg.GetOwner()
	if owner == "" {
		owner = "webui"
	}
	entry := memory.MemoryEntry{
		ID:        fmt.Sprintf("mem_%d", now.UnixNano()),
		Owner:     owner,
		ScopeType: memory.ScopePrivate,
		Content:   content,
		Tags:      req.Msg.GetTags(),
		Source:    memory.SourceManual,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.store.Save(entry); err != nil {
		return connect.NewResponse(&v1.AddMemoryResponse{
			Error: fmt.Sprintf("save failed: %v", err),
		}), nil
	}

	return connect.NewResponse(&v1.AddMemoryResponse{
		Memory: toProtoEntry(entry),
	}), nil
}

// UpdateMemory updates an existing memory entry's content and tags.
func (s *Service) UpdateMemory(
	ctx context.Context,
	req *connect.Request[v1.UpdateMemoryRequest],
) (*connect.Response[v1.UpdateMemoryResponse], error) {
	id := req.Msg.GetId()
	if id == "" {
		return connect.NewResponse(&v1.UpdateMemoryResponse{
			Success: false,
			Error:   "id is required",
		}), nil
	}

	updated := memory.MemoryEntry{
		ID:      id,
		Content: req.Msg.GetContent(),
		Tags:    req.Msg.GetTags(),
	}

	if req.Msg.GetScope() == "group" {
		groupID := req.Msg.GetGroupId()
		if groupID == "" {
			return connect.NewResponse(&v1.UpdateMemoryResponse{Success: false, Error: "group_id is required"}), nil
		}
		if s.groupManager == nil {
			return connect.NewResponse(&v1.UpdateMemoryResponse{Success: false, Error: "group memory manager not initialized"}), nil
		}
		groupStore, err := s.groupManager.GetGroupStore(groupID)
		if err != nil {
			return connect.NewResponse(&v1.UpdateMemoryResponse{Success: false, Error: err.Error()}), nil
		}
		if err := groupStore.UpdateEntry(updated); err != nil {
			return connect.NewResponse(&v1.UpdateMemoryResponse{Success: false, Error: err.Error()}), nil
		}
		return connect.NewResponse(&v1.UpdateMemoryResponse{Success: true}), nil
	}

	if err := s.store.UpdateEntry(updated); err != nil {
		return connect.NewResponse(&v1.UpdateMemoryResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.UpdateMemoryResponse{Success: true}), nil
}

// DeleteMemory removes a memory entry by ID.
func (s *Service) DeleteMemory(
	ctx context.Context,
	req *connect.Request[v1.DeleteMemoryRequest],
) (*connect.Response[v1.DeleteMemoryResponse], error) {
	id := req.Msg.GetId()
	if id == "" {
		return connect.NewResponse(&v1.DeleteMemoryResponse{
			Success: false,
			Error:   "id is required",
		}), nil
	}

	if req.Msg.GetScope() == "group" {
		groupID := req.Msg.GetGroupId()
		if groupID == "" {
			return connect.NewResponse(&v1.DeleteMemoryResponse{Success: false, Error: "group_id is required"}), nil
		}
		if s.groupManager == nil {
			return connect.NewResponse(&v1.DeleteMemoryResponse{Success: false, Error: "group memory manager not initialized"}), nil
		}
		groupStore, err := s.groupManager.GetGroupStore(groupID)
		if err != nil {
			return connect.NewResponse(&v1.DeleteMemoryResponse{Success: false, Error: err.Error()}), nil
		}
		if err := groupStore.Delete(id); err != nil {
			return connect.NewResponse(&v1.DeleteMemoryResponse{Success: false, Error: err.Error()}), nil
		}
		return connect.NewResponse(&v1.DeleteMemoryResponse{Success: true}), nil
	}

	if err := s.store.Delete(id); err != nil {
		return connect.NewResponse(&v1.DeleteMemoryResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}
	return connect.NewResponse(&v1.DeleteMemoryResponse{Success: true}), nil
}

// GetMemoryStats returns aggregate statistics about stored memories.
func (s *Service) GetMemoryStats(
	ctx context.Context,
	req *connect.Request[v1.GetMemoryStatsRequest],
) (*connect.Response[v1.GetMemoryStatsResponse], error) {
	scope := req.Msg.GetScope()

	if scope == "group" && req.Msg.GetGroupId() != "" {
		if s.groupManager == nil {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("group memory manager not initialized"))
		}
		groupStore, err := s.groupManager.GetGroupStore(req.Msg.GetGroupId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		entries, err := groupStore.ListAll()
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		byOwner := make(map[string]int32)
		for _, e := range entries {
			byOwner[e.Owner]++
		}
		return connect.NewResponse(&v1.GetMemoryStatsResponse{
			Total:           int32(len(entries)),
			GroupChatCount:  int32(len(entries)),
			GroupCount:      1,
			ByOwner:         byOwner,
		}), nil
	}

	privateEntries, err := s.store.ListAll()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list private memories: %w", err))
	}

	byOwner := make(map[string]int32)
	for _, e := range privateEntries {
		byOwner[e.Owner]++
	}

	groupChatCount := 0
	groupCount := 0
	if s.groupManager != nil {
		groupChatCount = s.groupManager.TotalMemoriesCount()
		if groups, err := s.groupManager.ListGroups(); err == nil {
			groupCount = len(groups)
		}
	}

	if scope == "private" {
		return connect.NewResponse(&v1.GetMemoryStatsResponse{
			Total:            int32(len(privateEntries)),
			PrivateChatCount: int32(len(privateEntries)),
			ByOwner:          byOwner,
		}), nil
	}

	resp := &v1.GetMemoryStatsResponse{
		Total:            int32(len(privateEntries) + groupChatCount),
		PrivateChatCount: int32(len(privateEntries)),
		GroupChatCount:   int32(groupChatCount),
		GroupCount:       int32(groupCount),
		ByOwner:          byOwner,
	}
	return connect.NewResponse(resp), nil
}

// ExportMemories returns memories as a JSON export string within the given scope.
func (s *Service) ExportMemories(
	ctx context.Context,
	req *connect.Request[v1.ExportMemoriesRequest],
) (*connect.Response[v1.ExportMemoriesResponse], error) {
	var entries []memory.MemoryEntry
	var err error

	if req.Msg.GetScope() == "group" {
		groupID := req.Msg.GetGroupId()
		if groupID == "" {
			return connect.NewResponse(&v1.ExportMemoriesResponse{Error: "group_id is required"}), nil
		}
		if s.groupManager == nil {
			return connect.NewResponse(&v1.ExportMemoriesResponse{Error: "group memory manager not initialized"}), nil
		}
		groupStore, gErr := s.groupManager.GetGroupStore(groupID)
		if gErr != nil {
			return connect.NewResponse(&v1.ExportMemoriesResponse{Error: gErr.Error()}), nil
		}
		entries, err = groupStore.ListAll()
	} else {
		entries, err = s.store.ListAll()
	}

	if err != nil {
		return connect.NewResponse(&v1.ExportMemoriesResponse{
			Error: fmt.Sprintf("list failed: %v", err),
		}), nil
	}

	data := memory.ExportData{
		Version:    1,
		Entries:    entries,
		ExportedAt: time.Now(),
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return connect.NewResponse(&v1.ExportMemoriesResponse{
			Error: fmt.Sprintf("marshal failed: %v", err),
		}), nil
	}

	return connect.NewResponse(&v1.ExportMemoriesResponse{
		JsonContent: string(raw),
	}), nil
}

// ImportMemories imports memories from a JSON export string into the given scope.
func (s *Service) ImportMemories(
	ctx context.Context,
	req *connect.Request[v1.ImportMemoriesRequest],
) (*connect.Response[v1.ImportMemoriesResponse], error) {
	jsonContent := req.Msg.GetJsonContent()
	if jsonContent == "" {
		return connect.NewResponse(&v1.ImportMemoriesResponse{
			Error: "json_content is required",
		}), nil
	}

	var data memory.ExportData
	if err := json.Unmarshal([]byte(jsonContent), &data); err != nil {
		return connect.NewResponse(&v1.ImportMemoriesResponse{
			Error: fmt.Sprintf("parse failed: %v", err),
		}), nil
	}

	overwrite := req.Msg.GetOverwrite()

	if req.Msg.GetScope() == "group" {
		groupID := req.Msg.GetGroupId()
		if groupID == "" {
			return connect.NewResponse(&v1.ImportMemoriesResponse{Error: "group_id is required"}), nil
		}
		if s.groupManager == nil {
			return connect.NewResponse(&v1.ImportMemoriesResponse{Error: "group memory manager not initialized"}), nil
		}
		groupStore, err := s.groupManager.GetGroupStore(groupID)
		if err != nil {
			return connect.NewResponse(&v1.ImportMemoriesResponse{Error: err.Error()}), nil
		}
		imported, skipped, err := groupStore.ImportData(data, overwrite)
		if err != nil {
			return connect.NewResponse(&v1.ImportMemoriesResponse{Error: fmt.Sprintf("import failed: %v", err)}), nil
		}
		return connect.NewResponse(&v1.ImportMemoriesResponse{
			Imported: int32(imported),
			Skipped:  int32(skipped),
		}), nil
	}

	imported, skipped, err := s.store.ImportData(data, overwrite)
	if err != nil {
		return connect.NewResponse(&v1.ImportMemoriesResponse{
			Error: fmt.Sprintf("import failed: %v", err),
		}), nil
	}

	return connect.NewResponse(&v1.ImportMemoriesResponse{
		Imported: int32(imported),
		Skipped:  int32(skipped),
	}), nil
}

// TriggerReflection starts owner-scoped reflection in the background.
func (s *Service) TriggerReflection(
	ctx context.Context,
	req *connect.Request[v1.TriggerReflectionRequest],
) (*connect.Response[v1.TriggerReflectionResponse], error) {
	if s.reflections == nil {
		return connect.NewResponse(&v1.TriggerReflectionResponse{
			Error: "memory reflection is not configured",
		}), nil
	}

	status, started, err := s.reflections.Start(req.Msg.GetOwner())
	if err != nil {
		return connect.NewResponse(&v1.TriggerReflectionResponse{
			Error: err.Error(),
		}), nil
	}
	return connect.NewResponse(&v1.TriggerReflectionResponse{
		Started:         started,
		Running:         status.Running,
		Owner:           status.Owner,
		StartedAt:       formatOptionalTime(status.StartedAt),
		LastCompletedAt: formatOptionalTime(status.LastCompletedAt),
		LastError:       status.LastError,
	}), nil
}

// ListGroups returns summaries of all observed QQ groups.
func (s *Service) ListGroups(
	ctx context.Context,
	req *connect.Request[v1.ListGroupsRequest],
) (*connect.Response[v1.ListGroupsResponse], error) {
	if s.groupManager == nil {
		return connect.NewResponse(&v1.ListGroupsResponse{Groups: []*v1.GroupSummary{}}), nil
	}

	summaries, err := s.groupManager.ListGroups()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list groups: %w", err))
	}

	result := make([]*v1.GroupSummary, len(summaries))
	for i, sm := range summaries {
		result[i] = &v1.GroupSummary{
			GroupId:     sm.GroupID,
			GroupName:   sm.GroupName,
			MemberCount: int32(sm.MemberCount),
			MemoryCount: int32(sm.MemoryCount),
			UpdatedAt:   sm.UpdatedAt.Format(time.RFC3339),
		}
	}
	return connect.NewResponse(&v1.ListGroupsResponse{Groups: result}), nil
}

// GetGroupProfile returns the full profile and member roster for a QQ group.
func (s *Service) GetGroupProfile(
	ctx context.Context,
	req *connect.Request[v1.GetGroupProfileRequest],
) (*connect.Response[v1.GetGroupProfileResponse], error) {
	groupID := req.Msg.GetGroupId()
	if groupID == "" {
		return connect.NewResponse(&v1.GetGroupProfileResponse{Error: "group_id is required"}), nil
	}
	if s.groupManager == nil {
		return connect.NewResponse(&v1.GetGroupProfileResponse{Error: "group memory manager not initialized"}), nil
	}

	groupStore, err := s.groupManager.GetGroupStore(groupID)
	if err != nil {
		return connect.NewResponse(&v1.GetGroupProfileResponse{Error: err.Error()}), nil
	}

	profile, err := groupStore.GetProfile()
	if err != nil {
		return connect.NewResponse(&v1.GetGroupProfileResponse{Error: err.Error()}), nil
	}

	var members []*v1.MemberProfile
	for _, m := range profile.Members {
		members = append(members, &v1.MemberProfile{
			UserId:        m.UserID,
			Nickname:      m.Nickname,
			Card:          m.Card,
			Role:          string(m.Role),
			PreferredName: m.PreferredName,
			Aliases:       m.Aliases,
			CallingName:   memory.ResolveCallingName(m),
			Source:        m.Source,
			CreatedAt:     m.CreatedAt.Format(time.RFC3339),
			UpdatedAt:     m.UpdatedAt.Format(time.RFC3339),
			LastSpokeAt:   m.LastSpokeAt.Format(time.RFC3339),
		})
	}

	// Sort members by last spoke descending
	sort.SliceStable(members, func(i, j int) bool {
		return members[i].LastSpokeAt > members[j].LastSpokeAt
	})

	return connect.NewResponse(&v1.GetGroupProfileResponse{
		Profile: &v1.GroupProfile{
			GroupId:   profile.GroupID,
			GroupName: profile.GroupName,
			Members:   members,
			UpdatedAt: profile.UpdatedAt.Format(time.RFC3339),
		},
	}), nil
}

// UpdateGroupProfile updates the group name.
func (s *Service) UpdateGroupProfile(
	ctx context.Context,
	req *connect.Request[v1.UpdateGroupProfileRequest],
) (*connect.Response[v1.UpdateGroupProfileResponse], error) {
	groupID := req.Msg.GetGroupId()
	if groupID == "" {
		return connect.NewResponse(&v1.UpdateGroupProfileResponse{Success: false, Error: "group_id is required"}), nil
	}
	if s.groupManager == nil {
		return connect.NewResponse(&v1.UpdateGroupProfileResponse{Success: false, Error: "group memory manager not initialized"}), nil
	}

	groupStore, err := s.groupManager.GetGroupStore(groupID)
	if err != nil {
		return connect.NewResponse(&v1.UpdateGroupProfileResponse{Success: false, Error: err.Error()}), nil
	}

	if err := groupStore.UpdateGroupName(req.Msg.GetGroupName()); err != nil {
		return connect.NewResponse(&v1.UpdateGroupProfileResponse{Success: false, Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.UpdateGroupProfileResponse{Success: true}), nil
}

// UpdateMemberProfile updates a member's preferred name and aliases.
func (s *Service) UpdateMemberProfile(
	ctx context.Context,
	req *connect.Request[v1.UpdateMemberProfileRequest],
) (*connect.Response[v1.UpdateMemberProfileResponse], error) {
	groupID := req.Msg.GetGroupId()
	userID := req.Msg.GetUserId()
	if groupID == "" || userID == "" {
		return connect.NewResponse(&v1.UpdateMemberProfileResponse{Success: false, Error: "group_id and user_id are required"}), nil
	}
	if s.groupManager == nil {
		return connect.NewResponse(&v1.UpdateMemberProfileResponse{Success: false, Error: "group memory manager not initialized"}), nil
	}

	groupStore, err := s.groupManager.GetGroupStore(groupID)
	if err != nil {
		return connect.NewResponse(&v1.UpdateMemberProfileResponse{Success: false, Error: err.Error()}), nil
	}

	if err := groupStore.UpdateMemberPreferredName(userID, req.Msg.GetPreferredName(), req.Msg.GetAliases()); err != nil {
		return connect.NewResponse(&v1.UpdateMemberProfileResponse{Success: false, Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.UpdateMemberProfileResponse{Success: true}), nil
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

// paginateEntries slices entries according to pagination params and returns ListMemoriesResponse.
func paginateEntries(entries []memory.MemoryEntry, pagination *v1.Pagination) (*v1.ListMemoriesResponse, error) {
	pageSize := int(pagination.GetPageSize())
	pageToken := pagination.GetPageToken()
	offset := 0
	if pageToken != "" {
		parsed, err := strconv.Atoi(pageToken)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("invalid page token %q", pageToken)
		}
		offset = parsed
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	total := len(entries)
	if offset > total {
		offset = total
	}
	end := total
	if pageSize < total-offset {
		end = offset + pageSize
	}
	page := entries[offset:end]

	nextToken := ""
	if end < total {
		nextToken = fmt.Sprintf("%d", end)
	}

	memories := make([]*v1.MemoryEntry, len(page))
	for i, e := range page {
		memories[i] = toProtoEntry(e)
	}

	return &v1.ListMemoriesResponse{
		Memories: memories,
		Pagination: &v1.Pagination{
			PageSize:  int32(pageSize),
			PageToken: nextToken,
			Total:     int32(total),
		},
	}, nil
}

// toProtoEntry converts a memory.MemoryEntry to a proto MemoryEntry.
func toProtoEntry(e memory.MemoryEntry) *v1.MemoryEntry {
	scope := string(e.ScopeType)
	if scope == "" {
		scope = string(memory.ScopePrivate)
	}
	return &v1.MemoryEntry{
		Id:          e.ID,
		Owner:       e.Owner,
		Content:     e.Content,
		Tags:        e.Tags,
		Source:      string(e.Source),
		Visibility:  string(e.Visibility),
		CreatedAt:   e.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   e.UpdatedAt.Format(time.RFC3339),
		AccessCount: int32(e.AccessCount),
		Scope:       scope,
		GroupId:     e.GroupID,
	}
}

// Ensure Service implements the interface at compile time.
var _ pbconnect.MemoryServiceHandler = (*Service)(nil)
