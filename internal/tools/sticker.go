package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"math/rand"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	stickerConfirmMaxCandidates       = 20
	stickerConfirmMaxOutputBytes      = llm.MaxToolOutputBytes / 2
	stickerConfirmMaxDescriptionBytes = 2048
	stickerConfirmMaxKeywords         = 8
	stickerConfirmMaxKeywordBytes     = 128
	stickerDeliveryGuidance           = "When sticker confirmation is enabled, first call send_sticker(query) alone in its own tool-call batch; this only retrieves candidates and sends nothing. After reviewing the candidates, if text should accompany the sticker, use a later tool-call batch with send_message (plain) immediately followed by send_sticker(id). Never pair send_message with the query-only call. If no text is needed, call send_sticker(id) alone. When confirmation is disabled, send_sticker(query) sends immediately, so text and sticker should use send_message (plain) immediately followed by send_sticker in the same batch. After successful delivery, do not repeat the sent text in the final response."
	stickerConfirmationHint           = "本次仅检索候选，未发送任何文字或贴纸。请结合描述选择 id：需要文字时，在下一批工具调用中先调用 send_message 发送 plain，紧接着调用 send_sticker(id)，将两者放在同一批；无需文字时，单独调用 send_sticker(id)。不要把 send_message 与仅检索的 send_sticker(query) 放在同一批。若 truncated 为 true，候选或描述、关键词已精简，可缩小 query 后单独重查。"
)

type compactStickerCandidate struct {
	ID          string   `json:"id"`
	Keywords    []string `json:"keywords"`
	Description string   `json:"description"`
}

type stickerConfirmationResponse struct {
	Candidates []compactStickerCandidate `json:"candidates"`
	Hint       string                    `json:"hint"`
	Truncated  bool                      `json:"truncated,omitempty"`
}

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
		description: "Search and send a sticker/meme image from the collected sticker library. Searches by emotion/context keywords (e.g. 开心, 生气, 无语, 嘲讽). Returns the selected sticker as an image message. 你被鼓励在合适的日常对话中使用贴纸增强情感表达。 " + stickerDeliveryGuidance,
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Emotion or context keyword to search for (e.g. '开心', '生气', '无语', '嘲讽'). Supports fuzzy matching. In confirmation mode, call this alone in its own batch to retrieve candidates without sending; send any accompanying text only in the later ID-confirmation batch.",
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
				resp := stickerConfirmationResponse{
					Candidates: make([]compactStickerCandidate, 0, stickerConfirmMaxCandidates),
					Hint:       stickerConfirmationHint,
					// Reserve the truncation flag while measuring the full JSON envelope.
					Truncated: true,
				}
				truncated := false
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
						if len(resp.Candidates) == stickerConfirmMaxCandidates {
							truncated = true
							break
						}
						candidate, compacted := compactStickerEntry(e)
						truncated = truncated || compacted
						resp.Candidates = append(resp.Candidates, candidate)
						data, _ := json.Marshal(resp)
						if len(data) > stickerConfirmMaxOutputBytes {
							resp.Candidates = resp.Candidates[:len(resp.Candidates)-1]
							truncated = true
							break
						}
					}
				}
				if len(resp.Candidates) == 0 && !truncated {
					return textResult(`{"error":"查无此词条"}`), nil
				}
				resp.Truncated = truncated
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

func compactStickerEntry(entry sticker.Entry) (compactStickerCandidate, bool) {
	description, truncated := compactStickerText(entry.Description, stickerConfirmMaxDescriptionBytes)
	candidate := compactStickerCandidate{ID: entry.ID, Description: description}
	for i, keyword := range entry.Keywords {
		if i == stickerConfirmMaxKeywords {
			truncated = true
			break
		}
		text, shortened := compactStickerText(keyword, stickerConfirmMaxKeywordBytes)
		candidate.Keywords = append(candidate.Keywords, text)
		truncated = truncated || shortened
	}
	return candidate, truncated
}

func compactStickerText(text string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return text, false
	}
	const marker = "…"
	cut := maxBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + marker, true
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
