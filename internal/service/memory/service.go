package memsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"

	"connectrpc.com/connect"
)

// Service implements frostagent.v1.MemoryServiceHandler.
type Service struct {
	store   *memory.Store
	vectors *memory.VectorStore
}

// New creates a new MemoryService.
func New(store *memory.Store, vectorStores ...*memory.VectorStore) *Service {
	service := &Service{store: store}
	if len(vectorStores) > 0 {
		service.vectors = vectorStores[0]
	}
	return service
}

// ListMemories returns a paginated list of memories, optionally filtered by owner.
func (s *Service) ListMemories(
	ctx context.Context,
	req *connect.Request[v1.ListMemoriesRequest],
) (*connect.Response[v1.ListMemoriesResponse], error) {
	owner := req.Msg.GetOwner()

	var entries []memory.MemoryEntry
	var err error
	if owner == "" {
		entries, err = s.store.ListAll()
	} else {
		entries, err = s.store.ListByOwner(owner)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list memories: %w", err))
	}

	resp := paginateEntries(entries, req.Msg.GetPagination())
	return connect.NewResponse(resp), nil
}

// SearchMemories performs keyword search across all memories.
// Supports optional tag filtering: if tags are provided, only entries matching
// at least one tag are returned.
func (s *Service) SearchMemories(
	ctx context.Context,
	req *connect.Request[v1.SearchMemoriesRequest],
) (*connect.Response[v1.SearchMemoriesResponse], error) {
	query := strings.TrimSpace(req.Msg.GetQuery())
	filterTags := normalizeTags(req.Msg.GetTags())
	if query == "" && len(filterTags) == 0 {
		return connect.NewResponse(&v1.SearchMemoriesResponse{}), nil
	}

	var entries []memory.MemoryEntry
	var err error
	if query == "" {
		entries, err = s.store.ListAll()
	} else {
		entries, err = s.store.Search(query, 0) // 0 = no limit
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("search memories: %w", err))
	}

	// Apply tag filter if provided
	if len(filterTags) > 0 {
		entries = filterByTags(entries, filterTags)
	}

	// Apply pagination
	resp := paginateEntries(entries, req.Msg.GetPagination())
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

	owner := req.Msg.GetOwner()
	if owner == "" {
		owner = "webui"
	}

	vis := memory.VisibilityPrivate
	if req.Msg.GetVisibility() == "public" {
		vis = memory.VisibilityPublic
	}

	now := time.Now()
	entry := memory.MemoryEntry{
		ID:         fmt.Sprintf("mem_%d", now.UnixNano()),
		Owner:      owner,
		Content:    content,
		Tags:       req.Msg.GetTags(),
		Source:     memory.SourceManual,
		Visibility: vis,
		Importance: 0.5,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.store.Save(entry); err != nil {
		return connect.NewResponse(&v1.AddMemoryResponse{
			Error: fmt.Sprintf("save failed: %v", err),
		}), nil
	}
	s.indexEntry(ctx, entry)

	return connect.NewResponse(&v1.AddMemoryResponse{
		Memory: toProtoEntry(entry),
	}), nil
}

// UpdateMemory updates an existing memory entry's content, tags, visibility, and importance.
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

	// Build the updated entry
	vis := memory.VisibilityPrivate
	if req.Msg.GetVisibility() == "public" {
		vis = memory.VisibilityPublic
	}

	updated := memory.MemoryEntry{
		ID:         id,
		Content:    req.Msg.GetContent(),
		Tags:       req.Msg.GetTags(),
		Visibility: vis,
		Importance: req.Msg.GetImportance(),
	}

	if err := s.store.UpdateEntry(updated); err != nil {
		return connect.NewResponse(&v1.UpdateMemoryResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}
	s.indexEntry(ctx, updated)

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

	if err := s.store.Delete(id); err != nil {
		return connect.NewResponse(&v1.DeleteMemoryResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}
	if s.vectors != nil {
		if err := s.vectors.Remove(id); err != nil {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("删除记忆向量失败 (id: %s): %v", id, err))
		}
	}
	return connect.NewResponse(&v1.DeleteMemoryResponse{Success: true}), nil
}

// GetMemoryStats returns aggregate statistics about stored memories.
func (s *Service) GetMemoryStats(
	ctx context.Context,
	req *connect.Request[v1.GetMemoryStatsRequest],
) (*connect.Response[v1.GetMemoryStatsResponse], error) {
	entries, err := s.store.ListAll()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list memories: %w", err))
	}

	byOwner := make(map[string]int32)
	pub, priv := int32(0), int32(0)
	for _, e := range entries {
		byOwner[e.Owner]++
		if e.Visibility == memory.VisibilityPublic {
			pub++
		} else {
			priv++
		}
	}

	resp := &v1.GetMemoryStatsResponse{
		Total:        int32(len(entries)),
		PublicCount:  pub,
		PrivateCount: priv,
		ByOwner:      byOwner,
	}
	return connect.NewResponse(resp), nil
}

// ExportMemories returns all memories as a JSON export string.
func (s *Service) ExportMemories(
	ctx context.Context,
	req *connect.Request[v1.ExportMemoriesRequest],
) (*connect.Response[v1.ExportMemoriesResponse], error) {
	entries, err := s.store.ListAll()
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

// ImportMemories imports memories from a JSON export string.
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
	imported, skipped, err := s.store.ImportData(data, overwrite)
	if err != nil {
		return connect.NewResponse(&v1.ImportMemoriesResponse{
			Error: fmt.Sprintf("import failed: %v", err),
		}), nil
	}
	if s.vectors != nil {
		entries, listErr := s.store.ListAll()
		if listErr != nil {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("导入后读取记忆失败，无法重建向量索引: %v", listErr))
		} else if rebuildErr := s.vectors.Rebuild(ctx, entries); rebuildErr != nil {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("导入后重建向量索引失败: %v", rebuildErr))
			if clearErr := s.vectors.Clear(); clearErr != nil {
				logs.Warn(logs.SYSTEM, fmt.Sprintf("清空过期向量索引失败: %v", clearErr))
			}
		}
	}

	return connect.NewResponse(&v1.ImportMemoriesResponse{
		Imported: int32(imported),
		Skipped:  int32(skipped),
	}), nil
}

// paginateEntries slices entries according to pagination params and returns ListMemoriesResponse.
func paginateEntries(entries []memory.MemoryEntry, pagination *v1.Pagination) *v1.ListMemoriesResponse {
	pageSize := int(pagination.GetPageSize())
	pageToken := pagination.GetPageToken()
	offset := 0
	if pageToken != "" {
		fmt.Sscanf(pageToken, "%d", &offset)
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	total := len(entries)
	end := offset + pageSize
	if end > total {
		end = total
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
	}
}

// toProtoEntry converts a memory.MemoryEntry to a proto MemoryEntry.
func toProtoEntry(e memory.MemoryEntry) *v1.MemoryEntry {
	return &v1.MemoryEntry{
		Id:         e.ID,
		Owner:      e.Owner,
		Content:    e.Content,
		Tags:       e.Tags,
		Source:     string(e.Source),
		Visibility: string(e.Visibility),
		Importance: e.Importance,
		CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  e.UpdatedAt.Format(time.RFC3339),
	}
}

// filterByTags returns entries that match at least one of the given tags (OR logic).
func filterByTags(entries []memory.MemoryEntry, tags []string) []memory.MemoryEntry {
	tagSet := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tagSet[tag] = true
	}
	var filtered []memory.MemoryEntry
	for _, e := range entries {
		for _, et := range e.Tags {
			if tagSet[strings.ToLower(strings.TrimSpace(et))] {
				filtered = append(filtered, e)
				break
			}
		}
	}
	return filtered
}

func normalizeTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" && !seen[tag] {
			normalized = append(normalized, tag)
			seen[tag] = true
		}
	}
	return normalized
}

func (s *Service) indexEntry(ctx context.Context, entry memory.MemoryEntry) {
	if s.vectors == nil {
		return
	}
	if err := s.vectors.IndexEntry(ctx, entry); err != nil {
		logs.Warn(logs.SYSTEM, fmt.Sprintf("同步记忆向量失败 (id: %s): %v", entry.ID, err))
		if !errors.Is(err, memory.ErrVectorIndexRebuildRequired) {
			if removeErr := s.vectors.Remove(entry.ID); removeErr != nil {
				logs.Warn(logs.SYSTEM, fmt.Sprintf("移除过期记忆向量失败 (id: %s): %v", entry.ID, removeErr))
			}
		}
	}
}

// Ensure Service implements the interface at compile time.
var _ pbconnect.MemoryServiceHandler = (*Service)(nil)
