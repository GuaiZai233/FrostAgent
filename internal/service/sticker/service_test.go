package stickersvc

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/sticker"
)

func TestStickerService_CRUD(t *testing.T) {
	tempDir := t.TempDir()
	store, err := sticker.NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	svc := New(store, nil)
	ctx := context.Background()

	// 1. Upload sticker
	uploadResp, err := svc.UploadSticker(ctx, connect.NewRequest(&v1.UploadStickerRequest{
		FileContent: []byte("fake_image_bytes_123"),
		Filename:    "test.jpg",
	}))
	if err != nil {
		t.Fatalf("UploadSticker failed: %v", err)
	}
	if !uploadResp.Msg.GetSuccess() {
		t.Fatalf("UploadSticker returned failure: %s", uploadResp.Msg.GetError())
	}
	stickerID := uploadResp.Msg.GetSticker().GetId()
	if stickerID == "" {
		t.Fatal("expected sticker ID, got empty")
	}

	// 2. Get stats
	statsResp, err := svc.GetStickerStats(ctx, connect.NewRequest(&v1.GetStickerStatsRequest{}))
	if err != nil {
		t.Fatalf("GetStickerStats failed: %v", err)
	}
	if statsResp.Msg.GetTotal() != 1 || statsResp.Msg.GetUnsummarized() != 1 {
		t.Errorf("unexpected stats: %+v", statsResp.Msg)
	}

	// 3. Update keywords
	updateResp, err := svc.UpdateStickerKeywords(ctx, connect.NewRequest(&v1.UpdateStickerKeywordsRequest{
		Id:          stickerID,
		Description: "一只开心的狐狸",
		Keywords:    []string{"开心", "狐狸", "笑"},
	}))
	if err != nil {
		t.Fatalf("UpdateStickerKeywords failed: %v", err)
	}
	if !updateResp.Msg.GetSuccess() {
		t.Fatalf("UpdateStickerKeywords returned failure: %s", updateResp.Msg.GetError())
	}

	// 4. List stickers
	listResp, err := svc.ListStickers(ctx, connect.NewRequest(&v1.ListStickersRequest{
		Search: "开心",
	}))
	if err != nil {
		t.Fatalf("ListStickers failed: %v", err)
	}
	if len(listResp.Msg.GetStickers()) != 1 {
		t.Fatalf("expected 1 sticker, got %d", len(listResp.Msg.GetStickers()))
	}
	if listResp.Msg.GetStickers()[0].GetDescription() != "一只开心的狐狸" {
		t.Errorf("unexpected description: %s", listResp.Msg.GetStickers()[0].GetDescription())
	}

	// 5. Get sticker image
	imgResp, err := svc.GetStickerImage(ctx, connect.NewRequest(&v1.GetStickerImageRequest{
		Id: stickerID,
	}))
	if err != nil {
		t.Fatalf("GetStickerImage failed: %v", err)
	}
	if string(imgResp.Msg.GetContent()) != "fake_image_bytes_123" {
		t.Errorf("image content mismatch")
	}

	// 6. Delete sticker
	delResp, err := svc.DeleteSticker(ctx, connect.NewRequest(&v1.DeleteStickerRequest{
		Id: stickerID,
	}))
	if err != nil {
		t.Fatalf("DeleteSticker failed: %v", err)
	}
	if !delResp.Msg.GetSuccess() {
		t.Fatalf("DeleteSticker returned failure: %s", delResp.Msg.GetError())
	}

	// Verify stats after deletion
	statsAfter, _ := svc.GetStickerStats(ctx, connect.NewRequest(&v1.GetStickerStatsRequest{}))
	if statsAfter.Msg.GetTotal() != 0 {
		t.Errorf("expected 0 stickers after deletion, got %d", statsAfter.Msg.GetTotal())
	}
}

func TestStickerService_Pagination(t *testing.T) {
	tempDir := t.TempDir()
	store, err := sticker.NewStore(filepath.Join(tempDir, "stickers"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	for i := 0; i < 5; i++ {
		data := []byte(fmt.Sprintf("image_data_%d", i))
		hash := sticker.HashBytes(data)
		if err := store.Add(hash, hash+".jpg", data); err != nil {
			t.Fatalf("Add %d failed: %v", i, err)
		}
	}

	svc := New(store, nil)
	ctx := context.Background()

	resp1, err := svc.ListStickers(ctx, connect.NewRequest(&v1.ListStickersRequest{
		Pagination: &v1.Pagination{PageSize: 2},
	}))
	if err != nil {
		t.Fatalf("page 1 failed: %v", err)
	}
	if len(resp1.Msg.GetStickers()) != 2 {
		t.Fatalf("page 1: expected 2 items, got %d", len(resp1.Msg.GetStickers()))
	}
	if resp1.Msg.GetTotalCount() != 5 {
		t.Errorf("expected total_count=5, got %d", resp1.Msg.GetTotalCount())
	}
	tok := resp1.Msg.GetNextPageToken()
	if tok == "" {
		t.Fatal("expected next_page_token, got empty")
	}

	resp2, err := svc.ListStickers(ctx, connect.NewRequest(&v1.ListStickersRequest{
		Pagination: &v1.Pagination{PageSize: 2, PageToken: tok},
	}))
	if err != nil {
		t.Fatalf("page 2 failed: %v", err)
	}
	if len(resp2.Msg.GetStickers()) != 2 {
		t.Fatalf("page 2: expected 2 items, got %d", len(resp2.Msg.GetStickers()))
	}

	for _, s1 := range resp1.Msg.GetStickers() {
		for _, s2 := range resp2.Msg.GetStickers() {
			if s1.GetId() == s2.GetId() {
				t.Errorf("duplicate sticker across pages: %s", s1.GetId())
			}
		}
	}

	tok2 := resp2.Msg.GetNextPageToken()
	if tok2 == "" {
		t.Fatal("expected next_page_token for page 3")
	}
	resp3, err := svc.ListStickers(ctx, connect.NewRequest(&v1.ListStickersRequest{
		Pagination: &v1.Pagination{PageSize: 2, PageToken: tok2},
	}))
	if err != nil {
		t.Fatalf("page 3 failed: %v", err)
	}
	if len(resp3.Msg.GetStickers()) != 1 {
		t.Fatalf("page 3: expected 1 item, got %d", len(resp3.Msg.GetStickers()))
	}
	if resp3.Msg.GetNextPageToken() != "" {
		t.Errorf("expected empty next_page_token on last page, got %q", resp3.Msg.GetNextPageToken())
	}
}
