package tools

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/sticker"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
)

const adminQQIDsEnv = "ADMIN_QQ_IDS"

func StealStickerTool(stealer *sticker.Stealer) Tool {
	return Tool{
		name: "steal_sticker",
		description: "Explicitly collect a QQ sticker attached to the current message. " +
			"Only use this when the current user explicitly asks to collect/steal the sticker and is a configured administrator. " +
			"The sticker_index is zero-based among sticker attachments in the current message; omit it when there is only one.",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sticker_index": map[string]any{
					"type":        "integer",
					"minimum":     0,
					"description": "Zero-based index of the sticker attachment in the current message. Defaults to 0.",
				},
			},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			if stealer == nil {
				return "", fmt.Errorf("sticker stealer is unavailable")
			}
			runContext, ok := llm.RunContextFromContext(ctx)
			if !ok || !isConfiguredQQAdmin(runContext.ActorUserID) {
				return "", fmt.Errorf("steal_sticker 仅允许 ADMIN_QQ_IDS 中配置的管理员使用")
			}

			var payload struct {
				StickerIndex int `json:"sticker_index"`
			}
			if strings.TrimSpace(args) != "" {
				decoder := json.NewDecoder(strings.NewReader(args))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&payload); err != nil {
					return "", fmt.Errorf("steal_sticker 参数不是合法 JSON: %w", err)
				}
			}
			if payload.StickerIndex < 0 || payload.StickerIndex >= len(runContext.StickerURLs) {
				return "", fmt.Errorf("当前消息中不存在索引为 %d 的可偷取 QQ 表情包", payload.StickerIndex)
			}

			result, err := stealer.Steal(ctx, runContext.StickerURLs[payload.StickerIndex])
			if err != nil {
				return "", fmt.Errorf("偷取表情包失败: %w", err)
			}
			response := struct {
				Success   bool   `json:"success"`
				StickerID string `json:"sticker_id"`
				Duplicate bool   `json:"duplicate"`
				Message   string `json:"message"`
			}{
				Success:   true,
				StickerID: result.ID,
				Duplicate: result.Duplicate,
				Message:   "sticker collected and queued for summary",
			}
			if result.Duplicate {
				response.Message = "sticker already existed; weight incremented"
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				return "", fmt.Errorf("序列化 steal_sticker 结果失败: %w", err)
			}
			return string(encoded), nil
		},
	}
}

func isConfiguredQQAdmin(userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	for _, configuredID := range strings.FieldsFunc(os.Getenv(adminQQIDsEnv), func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	}) {
		if configuredID == userID {
			return true
		}
	}
	return false
}
