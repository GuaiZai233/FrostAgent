package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
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
	IsSticker     bool   `json:"is_sticker,omitempty"`
}

// OneBotSegment 定义 OneBot v11 协议的标准消息段结构
type OneBotSegment struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"` // Data 字段的内容是动态的，所以用 map
}

func SendMsgTool() Tool {
	return Tool{
		name:        "send_message",
		description: "Send an ordered message chain immediately. Use this tool for media, quotes, proactive messages, or platform-native user mentions. Native mentions must use a `mention_user` component with the exact user ID; plain text cannot create a mention. Return ordinary text-only replies directly without this tool. Exception: when a sticker should follow the text in the same turn, call send_message first with a plain component, then call send_sticker in the same tool-call batch. Tool-call order is delivery order. After successful delivery, do not repeat the message in the final response.",
		//json schema
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"messages": map[string]any{
					"type":        "array",
					"description": "The ordered sequence of message components.",
					"minItems":    1,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"enum":        []string{"plain", "image", "record", "video", "file", "mention_user", "quote"},
								"description": "The component type. Use `mention_user` for a platform-native user mention.",
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
								"description": "The exact platform user ID to mention when type is `mention_user`. Use a trusted ID from the conversation context; never use a display name or invent an ID.",
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

		execute: func(args string) (string, error) {
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

func BuildOneBotMessage(toolMessages []Msg) ([]OneBotSegment, error) {
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
			fileData, err := buildOneBotMediaFile(Msg)
			if err != nil {
				return nil, err
			}

			segData := map[string]interface{}{"file": fileData}
			if Msg.Type == "image" && Msg.IsSticker {
				segData["sub_type"] = 1
				segData["subType"] = 1
			}
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: Msg.Type,
				Data: segData,
			})
		}
	}

	return oneBotChain, nil
}

func buildOneBotMediaFile(msg Msg) (string, error) {
	if msg.Type == "image" && msg.IsSticker && msg.Path != "" {
		content, err := os.ReadFile(msg.Path)
		if err != nil {
			return "", fmt.Errorf("读取贴纸文件 %q 失败: %w", msg.Path, err)
		}
		if len(content) == 0 {
			return "", fmt.Errorf("贴纸文件 %q 为空", msg.Path)
		}
		return "base64://" + base64.StdEncoding.EncodeToString(content), nil
	}

	if msg.Path != "" {
		return fmt.Sprintf("file://%s", msg.Path), nil
	}
	return msg.URL, nil
}
