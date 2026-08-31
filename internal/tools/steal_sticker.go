package tools

import (
	"FrostAgent/internal/llm"
	"FrostAgent/internal/sticker"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

const adminQQIDsEnv = "ADMIN_QQ_IDS"

func StealStickerTool(stealer *sticker.Stealer) Tool {
	return Tool{
		name: "steal_sticker",
		description: "Explicitly collect a QQ sticker from the trusted current-session message context. " +
			"Only use this when the current user explicitly asks to collect/steal the sticker and is a configured administrator. " +
			"Use message_id to select a current, replied-to, or recent message shown in trusted context. " +
			"If message_id is omitted, the latest sticker-bearing message is selected. sticker_index is zero-based within that message.",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message_id": map[string]any{
					"type":        "string",
					"description": "Message ID from system_context, reply_context, or recent_group_messages. Omit to use the latest sticker-bearing message.",
				},
				"sticker_index": map[string]any{
					"type":        "integer",
					"minimum":     0,
					"description": "Zero-based index of the sticker attachment within the selected message. Defaults to 0.",
				},
			},
			"additionalProperties": false,
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
				MessageID    string `json:"message_id"`
				StickerIndex int    `json:"sticker_index"`
			}
			if strings.TrimSpace(args) != "" {
				decoder := json.NewDecoder(strings.NewReader(args))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&payload); err != nil {
					return "", fmt.Errorf("steal_sticker 参数不是合法 JSON: %w", err)
				}
			}
			if payload.StickerIndex < 0 {
				return "", fmt.Errorf("sticker_index 不能小于 0")
			}

			result, resolvedMessageID, err := stealer.StealObserved(
				ctx,
				runContext.SessionID,
				strings.TrimSpace(payload.MessageID),
				payload.StickerIndex,
				runContext.LoadObservedSticker,
			)
			if err != nil {
				if errors.Is(err, sticker.ErrStickerNotInScope) {
					if strings.TrimSpace(payload.MessageID) == "" {
						return "", fmt.Errorf("当前会话上下文中不存在可偷取的 QQ 表情包")
					}
					return "", fmt.Errorf(
						"当前会话上下文的消息 %s 中不存在索引为 %d 的可偷取 QQ 表情包",
						strings.TrimSpace(payload.MessageID),
						payload.StickerIndex,
					)
				}
				return "", fmt.Errorf("偷取表情包失败: %w", err)
			}
			response := struct {
				Success   bool   `json:"success"`
				StickerID string `json:"sticker_id"`
				MessageID string `json:"message_id"`
				Duplicate bool   `json:"duplicate"`
				Message   string `json:"message"`
			}{
				Success:   true,
				StickerID: result.ID,
				MessageID: resolvedMessageID,
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
