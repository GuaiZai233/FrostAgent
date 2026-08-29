package tools

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/sticker"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestStealStickerToolRequiresConfiguredAdminAndCurrentSticker(t *testing.T) {
	t.Setenv(adminQQIDsEnv, "111, 222")
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	tool := StealStickerTool(sticker.NewStealer(store, nil))

	unauthorizedCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "22",
		StickerURLs: []string{"https://example.com/sticker.png"},
	})
	if _, err := tool.ExecuteContext(unauthorizedCtx, `{}`); err == nil || !strings.Contains(err.Error(), "仅允许") {
		t.Fatalf("unauthorized execution error = %v, want administrator denial", err)
	}

	adminWithoutStickerCtx := llm.WithRunContext(context.Background(), llm.RunContext{ActorUserID: "222"})
	if _, err := tool.ExecuteContext(adminWithoutStickerCtx, `{}`); err == nil || !strings.Contains(err.Error(), "当前消息中不存在") {
		t.Fatalf("missing sticker error = %v", err)
	}
	if _, err := tool.ExecuteContext(adminWithoutStickerCtx, `{"url":"https://example.com/untrusted.png"}`); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("model-supplied URL error = %v, want unknown field rejection", err)
	}
}

func TestStealStickerToolCollectsSelectedTrustedSticker(t *testing.T) {
	t.Setenv(adminQQIDsEnv, "123456")
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x02}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(imageBytes)
	}))
	defer server.Close()

	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	tool := StealStickerTool(sticker.NewStealer(store, nil))
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "123456",
		StickerURLs: []string{"http://127.0.0.1:1/must-not-use.png", server.URL + "/selected.png"},
	})

	output, err := tool.ExecuteContext(ctx, `{"sticker_index":1}`)
	if err != nil {
		t.Fatalf("admin execution returned error: %v", err)
	}
	var result struct {
		Success   bool   `json:"success"`
		StickerID string `json:"sticker_id"`
		Duplicate bool   `json:"duplicate"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("parse tool output: %v", err)
	}
	wantID := sticker.HashBytes(imageBytes)
	if !result.Success || result.StickerID != wantID || result.Duplicate {
		t.Fatalf("first tool result = %+v, want new sticker %s", result, wantID)
	}

	output, err = tool.ExecuteContext(ctx, `{"sticker_index":1}`)
	if err != nil {
		t.Fatalf("duplicate admin execution returned error: %v", err)
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("parse duplicate output: %v", err)
	}
	if !result.Duplicate {
		t.Fatalf("duplicate tool result = %+v, want duplicate=true", result)
	}
	entry, ok := store.Get(wantID)
	if !ok || entry.Weight != 2 {
		t.Fatalf("stored entry = %+v, found=%v; want weight 2", entry, ok)
	}
}
