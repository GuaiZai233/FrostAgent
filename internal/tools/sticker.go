package tools

import (
	"FrostAgent/internal/sticker"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
)

type stickerCandidate struct {
	entry  sticker.Entry
	weight int
}

func SendStickerTool(store *sticker.Store) Tool {
	return Tool{
		name:        "send_sticker",
		description: "Search and send a sticker/meme image from the collected sticker library. Searches by emotion/context keywords (e.g. 开心, 生气, 无语, 嘲讽). Returns the selected sticker as an image message. Use this when you want to react with a sticker that matches the conversation mood.",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Emotion or context keyword to search for (e.g. '开心', '生气', '无语', '嘲讽'). Supports fuzzy matching.",
				},
			},
			"required": []string{"query"},
		},
		execute: func(args string) (string, error) {
			var payload struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(args), &payload); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}
			query := strings.TrimSpace(payload.Query)
			if query == "" {
				return "", fmt.Errorf("query cannot be empty")
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
				return `{"error":"no matching sticker found"}`, nil
			}

			selected := weightedRandomSticker(candidates)
			filePath, ok := store.FilePath(selected.entry.ID)
			if !ok {
				return `{"error":"sticker file not found"}`, nil
			}
			imageURL := fmt.Sprintf("/api/sticker/%s/image", url.PathEscape(selected.entry.ID))

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
			return string(data), nil
		},
	}
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
