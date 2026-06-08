package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// msg 定义大模型工具返回的单条消息结构
type Msg struct {
	Type          string `json:"type"`
	Text          string `json:"text,omitempty"`
	MentionUserID string `json:"mention_user_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	Path          string `json:"path,omitempty"`
	URL           string `json:"url,omitempty"`
}

// OneBotSegment 定义 OneBot v11 协议的标准消息段结构
type OneBotSegment struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"` // Data 字段的内容是动态的，所以用 map
}

func SendMsgTool() Tool {
	return Tool{
		Name:        "send_message",
		Description: "Delivers messages (plain, image, record, video, file, mention_user) to the user. Strictly use this for sending media or initiating proactive tasks; standard text replies must be output directly without invoking this tool.",
		//json schema
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"messages": map[string]any{
					"type":        "array",
					"description": "A sequence of message payload objects. Include a `mention_user` component to ping a specific user.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"description": "The payload format. Allowed values: plain, image, record (voice audio), video, file, mention_user.",
							},
							"text": map[string]any{
								"type":        "string",
								"description": "The string content when type is `plain`.",
							},
							"path": map[string]any{
								"type":        "string",
								"description": "The local or sandbox file path required for media/file types.",
							},
							"url": map[string]any{
								"type":        "string",
								"description": "The web link for media/file types.",
							},
							"mention_user_id": map[string]any{
								"type":        "string",
								"description": "The identifier of the user to ping (used with `mention_user` type).",
							},
							"message_id": map[string]any{
								"type":        "string",
								"description": "The ID of the message to quote/reply to (required when type is `quote`).",
							},
						},
						"required": []string{"type"},
					},
				},
				"session": map[string]any{
					"type":        "string",
					"description": "Target session identifier (format: 'platform_id:message_type:session_id'). Omit to default to the active session.",
				},
			},
			"required": []string{"messages"},
		},

		Execute: func(args string) (string, error) {
			var payload struct {
				Messages []Msg  `json:"messages"`
				Session  string `json:"session,omitempty"`
			}

			if err := json.Unmarshal([]byte(args), &payload); err != nil {
				return "", fmt.Errorf("send_message 参数不是合法 JSON: %w", err)
			}
			if len(payload.Messages) == 0 {
				return "", fmt.Errorf("send_message 至少需要一条 messages")
			}

			for i, msg := range payload.Messages {
				switch msg.Type {
				case "plain":
					if strings.TrimSpace(msg.Text) == "" {
						return "", fmt.Errorf("messages[%d].text 不能为空", i)
					}
				case "mention_user":
					if strings.TrimSpace(msg.MentionUserID) == "" {
						return "", fmt.Errorf("messages[%d].mention_user_id 不能为空", i)
					}
				case "quote":
					if strings.TrimSpace(msg.MessageID) == "" {
						return "", fmt.Errorf("messages[%d].message_id 不能为空", i)
					}
				case "image", "record", "video", "file":
					if strings.TrimSpace(msg.Path) == "" && strings.TrimSpace(msg.URL) == "" {
						return "", fmt.Errorf("messages[%d] 需要 path 或 url", i)
					}
				default:
					return "", fmt.Errorf("messages[%d].type 不支持: %s", i, msg.Type)
				}
			}

			normalized, err := json.Marshal(payload)
			if err != nil {
				return "", fmt.Errorf("send_message 参数序列化失败: %w", err)
			}
			return string(normalized), nil
		},
	}
}

func BuildOneBotMessage(toolMessages []Msg) []OneBotSegment {
	var oneBotChain []OneBotSegment

	for _, Msg := range toolMessages {
		switch Msg.Type {
		case "plain":
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: "text",
				Data: map[string]interface{}{"text": " " + Msg.Text},
			})

		case "mention_user":
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: "at",
				Data: map[string]interface{}{"qq": Msg.MentionUserID},
			})

		case "quote":
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: "reply",
				// OneBot v11 协议中，引用的类型为 "reply"，参数字段为 "id"
				Data: map[string]interface{}{"id": Msg.MessageID},
			})

		case "image", "record", "video", "file":
			// 确定文件来源：URL 优先，如果有本地路径则拼接 file:// 前缀
			fileData := Msg.URL
			if Msg.Path != "" {
				fileData = fmt.Sprintf("file://%s", Msg.Path)
			}

			// OneBot 协议中图片、语音、视频的 type 名称与工具定义的正好一致
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: Msg.Type,
				Data: map[string]interface{}{"file": fileData},
			})
		}
	}

	return oneBotChain
}
