package llm

import (
	"encoding/json"
	"log"
	"strings"
)

func CallVisionModel(client *Client, baseURL, apiKey, modelName, contentBlocks string) string {
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

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: parsedContent}, // 传入真正的数组结构
	}

	log.Printf("即将传给：%s", messages)

	responseMsg, err := client.CallAPI(baseURL, apiKey, "qwen3.5-omni-plus-2026-03-15", messages, nil)
	if err != nil {
		return err.Error()
	}

	// 更加健壮地处理大模型的返回值，防止强转失败变成空字符串
	var contentStr string
	if str, ok := responseMsg.Content.(string); ok {
		contentStr = str
	} else if responseMsg.Content != nil {
		bytes, _ := json.Marshal(responseMsg.Content)
		contentStr = string(bytes)
		log.Printf("【警告】模型返回的 Content 不是纯文本，原始数据: %s", contentStr)
	} else {
		contentStr = "【视觉模型未返回任何描述内容】"
		log.Printf("【警告】视觉模型返回了空响应，完整对象: %+v", responseMsg)
	}

	log.Printf("调用视觉模型，描述：%s", contentStr)

	return contentStr
}
