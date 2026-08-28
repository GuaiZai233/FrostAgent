package stickersvc

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/sticker"
)

type Service struct {
	store      *sticker.Store
	summarizer *sticker.Summarizer
}

func New(store *sticker.Store, summarizer *sticker.Summarizer) *Service {
	return &Service{store: store, summarizer: summarizer}
}

func (s *Service) ListStickers(
	_ context.Context,
	req *connect.Request[v1.ListStickersRequest],
) (*connect.Response[v1.ListStickersResponse], error) {
	entries := s.store.List()

	statusFilter := req.Msg.GetStatusFilter()
	search := strings.TrimSpace(req.Msg.GetSearch())

	var filtered []sticker.Entry
	for _, e := range entries {
		if statusFilter != "" && string(e.Status) != statusFilter {
			continue
		}
		if search != "" {
			matched := strings.Contains(e.Description, search)
			if !matched {
				for _, kw := range e.Keywords {
					if strings.Contains(kw, search) || strings.Contains(search, kw) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
		}
		filtered = append(filtered, e)
	}

	pageSize := int(req.Msg.GetPagination().GetPageSize())
	if pageSize <= 0 {
		pageSize = 50
	}

	offset := 0
	if tok := req.Msg.GetPagination().GetPageToken(); tok != "" {
		if raw, err := base64.StdEncoding.DecodeString(tok); err == nil {
			if v, err := strconv.Atoi(string(raw)); err == nil && v >= 0 {
				offset = v
			}
		}
	}

	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}

	page := filtered[offset:end]
	items := make([]*v1.StickerItem, 0, len(page))
	for _, e := range page {
		items = append(items, entryToProto(e))
	}

	var nextPageToken string
	if end < len(filtered) {
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}

	return connect.NewResponse(&v1.ListStickersResponse{
		Stickers:      items,
		TotalCount:    int32(len(filtered)),
		NextPageToken: nextPageToken,
	}), nil
}

func (s *Service) DeleteSticker(
	_ context.Context,
	req *connect.Request[v1.DeleteStickerRequest],
) (*connect.Response[v1.DeleteStickerResponse], error) {
	id := req.Msg.GetId()
	if err := s.store.Delete(id); err != nil {
		return connect.NewResponse(&v1.DeleteStickerResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}
	return connect.NewResponse(&v1.DeleteStickerResponse{Success: true}), nil
}

func (s *Service) UpdateStickerKeywords(
	_ context.Context,
	req *connect.Request[v1.UpdateStickerKeywordsRequest],
) (*connect.Response[v1.UpdateStickerKeywordsResponse], error) {
	id := req.Msg.GetId()
	desc := req.Msg.GetDescription()
	keywords := req.Msg.GetKeywords()

	if err := s.store.Update(id, desc, keywords); err != nil {
		return connect.NewResponse(&v1.UpdateStickerKeywordsResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}
	return connect.NewResponse(&v1.UpdateStickerKeywordsResponse{Success: true}), nil
}

func (s *Service) UploadSticker(
	_ context.Context,
	req *connect.Request[v1.UploadStickerRequest],
) (*connect.Response[v1.UploadStickerResponse], error) {
	data := req.Msg.GetFileContent()
	filename := req.Msg.GetFilename()
	if len(data) == 0 {
		return connect.NewResponse(&v1.UploadStickerResponse{
			Success: false,
			Error:   "file content is empty",
		}), nil
	}

	hash := sticker.HashBytes(data)
	if s.store.Exists(hash) {
		_ = s.store.IncrementWeight(hash)
		entry, _ := s.store.Get(hash)
		return connect.NewResponse(&v1.UploadStickerResponse{
			Success: true,
			Sticker: entryToProto(entry),
		}), nil
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}
	storedName := hash + ext

	if err := s.store.Add(hash, storedName, data); err != nil {
		return connect.NewResponse(&v1.UploadStickerResponse{
			Success: false,
			Error:   fmt.Sprintf("add sticker: %v", err),
		}), nil
	}

	if s.summarizer != nil {
		s.summarizer.Enqueue(hash)
	}

	entry, _ := s.store.Get(hash)
	return connect.NewResponse(&v1.UploadStickerResponse{
		Success: true,
		Sticker: entryToProto(entry),
	}), nil
}

func (s *Service) RetryAllUnsummarized(
	_ context.Context,
	_ *connect.Request[v1.RetryAllUnsummarizedRequest],
) (*connect.Response[v1.RetryAllUnsummarizedResponse], error) {
	count := 0
	if s.summarizer != nil {
		count = s.summarizer.EnqueueUnsummarized()
	}
	return connect.NewResponse(&v1.RetryAllUnsummarizedResponse{
		EnqueuedCount: int32(count),
	}), nil
}

func (s *Service) GetStickerStats(
	_ context.Context,
	_ *connect.Request[v1.GetStickerStatsRequest],
) (*connect.Response[v1.GetStickerStatsResponse], error) {
	stats := s.store.Stats()
	return connect.NewResponse(&v1.GetStickerStatsResponse{
		Total:        int32(stats.Total),
		Ready:        int32(stats.Ready),
		Unsummarized: int32(stats.Unsummarized),
	}), nil
}

func (s *Service) GetStickerImage(
	_ context.Context,
	req *connect.Request[v1.GetStickerImageRequest],
) (*connect.Response[v1.GetStickerImageResponse], error) {
	id := req.Msg.GetId()
	fp, ok := s.store.FilePath(id)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sticker %s not found", id))
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read file: %w", err))
	}

	entry, _ := s.store.Get(id)
	ct := "image/jpeg"
	lower := strings.ToLower(entry.FileName)
	switch {
	case strings.HasSuffix(lower, ".gif"):
		ct = "image/gif"
	case strings.HasSuffix(lower, ".png"):
		ct = "image/png"
	case strings.HasSuffix(lower, ".webp"):
		ct = "image/webp"
	}

	return connect.NewResponse(&v1.GetStickerImageResponse{
		Content:     data,
		ContentType: ct,
	}), nil
}

// ImageHandler serves sticker image files over HTTP GET /api/sticker/{id}/image.
func (s *Service) ImageHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		// Expecting "api", "sticker", "<id>", "image" or "api", "sticker", "<id>"
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		id := parts[2]
		fp, ok := s.store.FilePath(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		entry, _ := s.store.Get(id)
		ct := "image/jpeg"
		lower := strings.ToLower(entry.FileName)
		switch {
		case strings.HasSuffix(lower, ".gif"):
			ct = "image/gif"
		case strings.HasSuffix(lower, ".png"):
			ct = "image/png"
		case strings.HasSuffix(lower, ".webp"):
			ct = "image/webp"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, fp)
	}
}

func entryToProto(e sticker.Entry) *v1.StickerItem {
	return &v1.StickerItem{
		Id:          e.ID,
		FileName:    e.FileName,
		Description: e.Description,
		Keywords:    e.Keywords,
		Weight:      int32(e.Weight),
		Status:      string(e.Status),
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}
