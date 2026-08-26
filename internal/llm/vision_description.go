package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"context"
	"encoding/json"
	"strings"
)

func CallVisionModel(ctx context.Context, provider core.LLMProvider, route core.RouteContext, contentBlocks string) string {
	systemPrompt := "请用中文描述图片："

	// 将传入的 JSON 字符串格式反序列化为真正的对象数组
	var parsedContent any = contentBlocks
	var blocks []any
	if err := json.Unmarshal([]byte(contentBlocks), &blocks); err == nil {
		// 修复空 text block 导致的无响应问题
		for i, b := range blocks {
			if m, ok := b.(map[string]any); ok {
				if m["type"] == "text" {
					textVal, _ := m["text"].(string)
					if strings.TrimSpace(textVal) == "" {
						m["text"] = "请详细描述这张图片的内容"
					}
					blocks[i] = m
				}
			}
		}
		parsedContent = blocks // 解析成功，使用对象数组
	}

	chatReq := core.ChatRequest{
		Messages: []core.ChatMessage{
			// 系统提示词：告诉模型它是来干嘛的
			{Role: core.RoleSystem, Content: systemPrompt},
			// 用户内容：这里就是你传入的包含文字和图片 Base64 的 parsedContent
			{Role: core.RoleUser, Content: parsedContent},
		},
		Route: route,
	}

	logs.Info(logs.SYSTEM, "即将传递消息给视觉模型")

	// 调用 LLMProvider 接口
	resp, err := provider.Chat(ctx, chatReq)
	if err != nil {
		logs.Error(logs.SYSTEM, "视觉模型调用失败: "+err.Error())
		return ""
	}

	// 更加健壮地处理大模型的返回值，防止强转失败变成空字符串
	var contentStr string
	// 尝试把 Content 转成字符串（大部分模型返回的是纯文本）
	if str, ok := resp.Message.Content.(string); ok {
		contentStr = str
	} else if resp.Message.Content != nil {
		// 如果返回的是复杂的 ContentPart 数组，就序列化成 JSON 兜底
		bytes, _ := json.Marshal(resp.Message.Content)
		contentStr = string(bytes)
	} else {
		contentStr = "【视觉模型未返回内容】"
	}

	logs.Info(logs.SYSTEM, "调用视觉模型，描述："+contentStr)

	return contentStr
}
