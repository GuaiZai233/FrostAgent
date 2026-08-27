package sticker

import (
	"FrostAgent/internal/core"
	"context"
	"fmt"
)

type LLMVisionCaller struct {
	Provider  core.LLMProvider
	ModelName string
}

func (v *LLMVisionCaller) Describe(imageBase64, mimeType string) (string, []string, error) {
	if v.Provider == nil {
		return "", nil, fmt.Errorf("vision provider not configured")
	}

	model := v.ModelName
	if model == "" {
		return "", nil, fmt.Errorf("vision model not configured")
	}

	contentParts := []map[string]any{
		{
			"type": "image_url",
			"image_url": map[string]string{
				"url": fmt.Sprintf("data:%s;base64,%s", mimeType, imageBase64),
			},
		},
		{
			"type": "text",
			"text": `请分析这张表情包/贴纸图片，返回严格的JSON格式：
{"description":"一句话描述图片内容和表达的情绪","keywords":["关键词1","关键词2","关键词3"]}
关键词应为中文情绪/语境词，如：开心、生气、无语、尴尬、嘲讽、惊讶、期待、可爱等。返回3-6个关键词。只返回JSON，不要其他文字。`,
		},
	}

	req := core.ChatRequest{
		Model: model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: contentParts},
		},
	}

	resp, err := v.Provider.Chat(context.Background(), req)
	if err != nil {
		return "", nil, fmt.Errorf("vision call: %w", err)
	}

	var raw string
	if str, ok := resp.Message.Content.(string); ok {
		raw = str
	} else {
		return "", nil, fmt.Errorf("unexpected response type")
	}

	desc, keywords := ParseVisionResult(raw)
	if desc == "" {
		return "", nil, fmt.Errorf("empty description from vision model")
	}
	return desc, keywords, nil
}
