package tools

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/sticker"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestStealStickerToolRequiresConfiguredAdminAndTrustedContext(t *testing.T) {
	t.Setenv(adminQQIDsEnv, "111, 222")
	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	tool := StealStickerTool(sticker.NewStealer(store, nil))

	unauthorizedCtx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "22",
		SessionID:   "private:22",
	})
	if _, err := tool.ExecuteContext(unauthorizedCtx, `{}`); err == nil || !strings.Contains(err.Error(), "仅允许") {
		t.Fatalf("unauthorized execution error = %v, want administrator denial", err)
	}

	adminWithoutStickerCtx := llm.WithRunContext(context.Background(), llm.RunContext{ActorUserID: "222"})
	if _, err := tool.ExecuteContext(adminWithoutStickerCtx, `{}`); err == nil || !strings.Contains(err.Error(), "当前会话上下文中不存在") {
		t.Fatalf("missing sticker error = %v", err)
	}
	if _, err := tool.ExecuteContext(adminWithoutStickerCtx, `{"url":"https://example.com/untrusted.png"}`); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("model-supplied URL error = %v, want unknown field rejection", err)
	}
}

func TestStealStickerToolCollectsSelectedHistoricalStickerBytes(t *testing.T) {
	t.Setenv(adminQQIDsEnv, "123456")
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x02}

	store, err := sticker.NewStore(filepath.Join(t.TempDir(), "stickers"))
	if err != nil {
		t.Fatalf("create sticker store: %v", err)
	}
	stealer := sticker.NewStealer(store, nil)
	stealer.Observe(
		"group:7788",
		"msg_older",
		0,
		nil,
		false,
	)
	tool := StealStickerTool(stealer)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{
		ActorUserID: "123456",
		SessionID:   "group:7788",
		LoadObservedSticker: func(context.Context, string, int) ([]byte, error) {
			return append([]byte(nil), imageBytes...), nil
		},
	})

	output, err := tool.ExecuteContext(ctx, `{"message_id":"msg_older"}`)
	if err != nil {
		t.Fatalf("admin execution returned error: %v", err)
	}
	var result struct {
		Success   bool   `json:"success"`
		StickerID string `json:"sticker_id"`
		MessageID string `json:"message_id"`
		Duplicate bool   `json:"duplicate"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("parse tool output: %v", err)
	}
	wantID := sticker.HashBytes(imageBytes)
	if !result.Success || result.StickerID != wantID || result.MessageID != "msg_older" || result.Duplicate {
		t.Fatalf("first tool result = %+v, want new sticker %s", result, wantID)
	}

	output, err = tool.ExecuteContext(ctx, `{}`)
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
