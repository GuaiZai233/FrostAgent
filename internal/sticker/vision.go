package sticker

import (
	"FrostAgent/internal/core"
	"context"
	"fmt"
	"time"
)

type LLMVisionCaller struct {
	Provider  core.LLMProvider
	ModelName string
}

func (v *LLMVisionCaller) Describe(imageBase64, mimeType string) (string, []string, bool, error) {
	if v.Provider == nil {
		return "", nil, false, fmt.Errorf("vision provider not configured")
	}

	model := v.ModelName
	if model == "" {
		return "", nil, false, fmt.Errorf("vision model not configured")
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
{"description":"一句话描述图片内容和表达的情绪","keywords":["关键词1","关键词2","关键词3"],"suspected_inappropriate":false}
关键词应为中文情绪/语境词，如：开心、生气、无语、尴尬、嘲讽、惊讶、期待、可爱等。返回3-6个关键词。
如果图片疑似包含R-18或软色情（涩涩、敏感部位、性暗示等）、政治敏感内容、极端血腥暴力、仇恨歧视或其他明显不适合Bot主动发送的内容，将suspected_inappropriate设为true；否则设为false。疑似时也要正常返回描述和关键词。只返回JSON，不要其他文字。`,
		},
	}

	req := core.ChatRequest{
		Model: model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: contentParts},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := v.Provider.Chat(ctx, req)
	if err != nil {
		return "", nil, false, fmt.Errorf("vision call: %w", err)
	}

	var raw string
	if str, ok := resp.Message.Content.(string); ok {
		raw = str
	} else {
		return "", nil, false, fmt.Errorf("unexpected response type")
	}

	desc, keywords, suspectedInappropriate, err := ParseVisionResult(raw)
	if err != nil {
		return "", nil, false, err
	}
	return desc, keywords, suspectedInappropriate, nil
}
