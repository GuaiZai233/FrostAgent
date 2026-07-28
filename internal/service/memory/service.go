package memsvc

import (
	"context"
	"fmt"
	"time"

	"FrostAgent/internal/memory"
	v1 "FrostAgent/gen/proto/frostagent/v1"
	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"

	"connectrpc.com/connect"
)

// Service implements frostagent.v1.MemoryServiceHandler.
type Service struct {
	store *memory.Store
}

// New creates a new MemoryService.
func New(store *memory.Store) *Service {
	return &Service{store: store}
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

	// Pagination
	pageSize := int(req.Msg.GetPagination().GetPageSize())
	pageToken := req.Msg.GetPagination().GetPageToken()
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

	resp := &v1.ListMemoriesResponse{
		Memories: memories,
		Pagination: &v1.Pagination{
			PageSize:  int32(pageSize),
			PageToken: nextToken,
			Total:     int32(total),
		},
	}
	return connect.NewResponse(resp), nil
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

// Ensure Service implements the interface at compile time.
var _ pbconnect.MemoryServiceHandler = (*Service)(nil)