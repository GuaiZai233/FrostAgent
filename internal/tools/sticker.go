package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"math/rand"
	"net/url"
	"strings"
)

type stickerCandidate struct {
	entry  sticker.Entry
	weight int
}

func SendStickerTool(store *sticker.Store, prefixes ...string) Tool {
	prefix := ""
	if len(prefixes) > 0 {
		prefix = prefixes[0]
	}
	return Tool{
		name:        "send_sticker",
		description: "Search and send a sticker/meme image from the collected sticker library. Searches by emotion/context keywords (e.g. 开心, 生气, 无语, 嘲讽). Returns the selected sticker as an image message. When sticker confirmation is enabled, passing 'query' returns candidate stickers with descriptions, and you must call send_sticker again with 'id' to confirm and send the chosen sticker. When sticker confirmation is disabled, passing 'query' directly selects and sends a matching sticker. Use this when you want to react with a sticker that matches the conversation mood. 你被鼓励在合适的日常对话中使用贴纸增强情感表达。需要同时发送文本和表情包时，必须在同一批工具调用中先调用 send_message 发送 plain，再调用 send_sticker；工具调用顺序即发送顺序，禁止先调用 send_sticker。若无需文本，可以单独调用 send_sticker。成功发送后不要在最终回复中重复已经发送的文本。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Emotion or context keyword to search for (e.g. '开心', '生气', '无语', '嘲讽'). Supports fuzzy matching. In confirmation mode, this returns candidate stickers without sending.",
				},
				"id": map[string]any{
					"type":        "string",
					"description": "Specific sticker ID to select and send. Required in confirmation mode after reviewing candidate stickers.",
				},
			},
		},
		executeResult: func(ctx context.Context, args string) (core.ToolExecutionResult, error) {
			if sticker.ActivityLevel(runtimescope.FromContext(ctx).Getenv(sticker.ActivityEnvKey)) == 0 {
				return textResult(`{"error":"表情包发送功能已被禁用，请管理员在 FrostAgent 后端启用此功能。"}`), nil
			}
			var payload struct {
				Query string `json:"query"`
				ID    string `json:"id"`
			}
			if err := json.Unmarshal([]byte(args), &payload); err != nil {
				return textResult(""), fmt.Errorf("invalid args: %w", err)
			}
			query := strings.TrimSpace(payload.Query)
			id := strings.TrimSpace(payload.ID)

			confirmEnabled := sticker.ConfirmEnabled(runtimescope.FromContext(ctx).Getenv(sticker.ConfirmEnvKey))

			if confirmEnabled {
				if id != "" {
					return stickerImageResult(store, prefix, id)
				}
				if query == "" {
					return textResult(""), fmt.Errorf("query cannot be empty")
				}
				entries := store.List()
				type compactCandidate struct {
					ID          string   `json:"id"`
					Keywords    []string `json:"keywords"`
					Description string   `json:"description"`
				}
				var candidates []compactCandidate
				for _, e := range entries {
					if e.Status != sticker.StatusReady || e.SuspectedInappropriate || e.Weight <= 0 {
						continue
					}
					matched := false
					for _, kw := range e.Keywords {
						if strings.Contains(kw, query) || strings.Contains(query, kw) {
							matched = true
							break
						}
					}
					if !matched && strings.Contains(e.Description, query) {
						matched = true
					}
					if matched {
						candidates = append(candidates, compactCandidate{
							ID:          e.ID,
							Keywords:    e.Keywords,
							Description: e.Description,
						})
					}
				}
				if len(candidates) == 0 {
					return textResult(`{"error":"查无此词条"}`), nil
				}
				resp := struct {
					Candidates []compactCandidate `json:"candidates"`
					Hint       string             `json:"hint"`
				}{
					Candidates: candidates,
					Hint:       "已找到满足条件的候选表情包。请结合对话语境与上述具体描述，再次调用 send_sticker 并传入所选表情包的 id 以完成发送。",
				}
				data, _ := json.Marshal(resp)
				return core.ToolExecutionResult{Content: string(data)}, nil
			}

			if id != "" {
				return stickerImageResult(store, prefix, id)
			}
			if query == "" {
				return textResult(""), fmt.Errorf("query cannot be empty")
			}

			entries := store.List()
			var candidates []stickerCandidate

			for _, e := range entries {
				if e.Status != sticker.StatusReady || e.SuspectedInappropriate || e.Weight <= 0 {
					continue
				}
				matched := false
				for _, kw := range e.Keywords {
					if strings.Contains(kw, query) || strings.Contains(query, kw) {
						matched = true
						break
					}
				}
				if !matched && strings.Contains(e.Description, query) {
					matched = true
				}
				if matched {
					candidates = append(candidates, stickerCandidate{entry: e, weight: e.Weight})
				}
			}

			if len(candidates) == 0 {
				return textResult(`{"error":"查无此词条"}`), nil
			}

			selected := weightedRandomSticker(candidates)
			return stickerImageResult(store, prefix, selected.entry.ID)
		},
	}
}

func stickerImageResult(store *sticker.Store, prefix, id string) (core.ToolExecutionResult, error) {
	entry, ok := store.Get(id)
	if !ok || entry.Status != sticker.StatusReady || entry.SuspectedInappropriate || entry.Weight <= 0 {
		return textResult(`{"error":"指定表情包不存在或不可用"}`), nil
	}
	filePath, ok := store.FilePath(id)
	if !ok {
		return textResult(`{"error":"sticker file not found"}`), nil
	}
	imageURL := fmt.Sprintf(prefix+"/api/sticker/%s/image", url.PathEscape(id))

	result := struct {
		Messages []Msg `json:"messages"`
	}{
		Messages: []Msg{
			{
				Type:      "image",
				Path:      filePath,
				URL:       imageURL,
				IsSticker: true,
			},
		},
	}
	data, _ := json.Marshal(result)
	return core.ToolExecutionResult{Content: string(data), MessagePayload: string(data)}, nil
}

func weightedRandomSticker(candidates []stickerCandidate) stickerCandidate {
	totalWeight := 0
	for _, c := range candidates {
		w := c.weight
		if w < 1 {
			w = 1
		}
		totalWeight += w
	}
	r := rand.Intn(totalWeight)
	for _, c := range candidates {
		w := c.weight
		if w < 1 {
			w = 1
		}
		r -= w
		if r < 0 {
			return c
		}
	}
	return candidates[0]
}
