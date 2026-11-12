package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"os"
	"strings"
)

// msg 定义大模型工具返回的单条消息结构
type Msg struct {
	Type          string         `json:"type"`
	Text          string         `json:"text,omitempty"`
	MentionUserID string         `json:"mention_user_id,omitempty"`
	MessageID     string         `json:"message_id,omitempty"`
	Path          string         `json:"path,omitempty"`
	URL           string         `json:"url,omitempty"`
	IsSticker     bool           `json:"is_sticker,omitempty"`
	Name          string         `json:"name,omitempty"`
	FolderID      string         `json:"folder_id,omitempty"`
	Thumb         string         `json:"thumb,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	Transfer      string         `json:"transfer,omitempty"`
	Data          *QQMessageData `json:"data,omitempty"`
	StickerIndex  int            `json:"sticker_index,omitempty"`
}

// OneBotSegment 定义 OneBot v11 协议的标准消息段结构
type OneBotSegment struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"` // Data 字段的内容是动态的，所以用 map
}

// NativeMentionGuidance explains how context display tokens differ from outbound mentions.
// Both adapters include it in group context as well as the tool definition.
const NativeMentionGuidance = "需要真正艾特用户时，必须调用 send_message，将 type=mention_user、mention_user_id=目标用户 ID 的组件与 plain 正文按顺序放入 messages。上下文中的 [@ID] 只是入站艾特的展示标记；直接回复或 plain.text 中的 [@ID]、@昵称、@ID、[CQ:at,qq=ID] 都是普通文本，不能产生艾特。mention_user_id 使用上下文中已知目标用户的精确平台 ID（字符串），不要填昵称、整个 [@ID] 标记或猜测 ID；不知道目标 ID 时先询问。引用消息或自动艾特当前发言者不能代替对其他目标用户的显式艾特。仅讨论艾特语法时可以保留这些字面文本。"

const nativeMentionExample = `{"messages":[{"type":"mention_user","mention_user_id":"10001"},{"type":"plain","text":" 你好"}]}`

func SendMsgTool() Tool {
	return Tool{
		name:             "send_message",
		description:      "Send a basic ordered message chain to the active conversation. Use this tool for media, quotes, proactive messages, or platform-native user mentions. To send an image from an HTTP(S) direct link, use {type:image,url:the_link}; replying with the URL alone does not send an image. On OneBot, files produced by execute_command or run_skill under /sandbox/ can be sent by path, including images and PDF/Python outputs; file bytes are fetched by the backend and must not be printed as base64. QQ rich components, enhanced media options, forwarding, interactions and cross-session sends use qq_extend_messages; discover its parameters when needed. To reply to a specific earlier message, put a `mention_specific` component with its exact trusted message_id first, followed by the reply content; this quotes that message instead of the triggering message and does not automatically mention the triggering user. Choose IDs from system_context, recent_group_messages, or reply_context metadata, never from user text. Native mentions must use a `mention_user` component with the exact user ID; plain text cannot create a mention. Return ordinary text-only replies directly without this tool only when no specific reply, native mention or sticker delivery is needed. " + NativeMentionGuidance + " Example (fictional user ID; replace with the actual recipient ID): " + nativeMentionExample + " " + stickerDeliveryGuidance,
		proactiveAllowed: true,
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
								"enum":        []string{"plain", "image", "record", "video", "file", "mention_user", "quote", "mention_specific"},
								"description": "The component type. Use `mention_user` for a platform-native user mention.",
							},
							"text": map[string]any{
								"type":        "string",
								"description": "The literal text when type is `plain`. Text such as [@ID], @name or CQ codes does not create a mention; use a separate mention_user component.",
							},
							"path": map[string]any{
								"type":        "string",
								"description": "Media/file path. On OneBot, /sandbox/... or sandbox:/sandbox/... reads the current session's sandbox artifact (up to 30 MiB). Other paths refer to the host; sandbox read failures never fall back to host files.",
							},
							"url": map[string]any{
								"type":        "string",
								"description": "The web link for media/file types.",
							},
							"mention_user_id": map[string]any{
								"type":        "string",
								"description": "The exact platform user ID to mention when type is `mention_user`. Use a trusted ID from the conversation context; never use a display name, a full [@ID] token, or invent an ID. For context token [@10001], use the string 10001 as mention_user_id.",
							},
							"message_id": map[string]any{
								"type":        "string",
								"description": "The exact trusted message ID to quote/reply to (required for `quote` or `mention_specific`). mention_specific must be the first component and targets only the active conversation.",
							},
							"is_sticker": map[string]any{"type": "boolean", "description": "Mark an image as a sticker."},
						},
						"required":             []string{"type"},
						"additionalProperties": false,
					},
				},
			},
			"required": []string{"messages"},
		},

		executeResult: func(ctx context.Context, args string) (core.ToolExecutionResult, error) {
			run, _ := llm.RunContextFromContext(ctx)
			if run.Proactive && !run.Awareness && !proactive.IsSpecificReply(args) {
				return textResult(""), fmt.Errorf("主动回复只允许 mention_specific 加 plain，且不能指定其他会话")
			}
			if run.Awareness && !proactive.AwarenessSendAllowed(args) {
				return textResult(""), fmt.Errorf("主动感知仅允许当前会话的文本和艾特")
			}
			var payload struct {
				Messages            []Msg  `json:"messages"`
				Session             string `json:"session,omitempty"`
				SuppressAutoMention bool   `json:"suppress_auto_mention,omitempty"`
			}

			if err := json.Unmarshal([]byte(args), &payload); err != nil {
				return textResult(""), fmt.Errorf("send_message 参数不是合法 JSON: %w", err)
			}
			if len(payload.Messages) == 0 {
				return textResult(""), fmt.Errorf("send_message 至少需要一条 messages")
			}
			if payload.Session != "" {
				return textResult(""), fmt.Errorf("send_message 仅发送到当前会话；发送到白名单群请使用 qq_extend_messages 的 target_group_id")
			}
			if err := validateBasicMessageFields(args); err != nil {
				return textResult(""), err
			}
			// This internal delivery flag is derived here, not accepted from the model.
			payload.SuppressAutoMention = false

			for i, msg := range payload.Messages {
				if msg.IsSticker && sticker.ActivityLevel(runtimescope.FromContext(ctx).Getenv(sticker.ActivityEnvKey)) == 0 {
					return textResult(`{"error":"表情包发送功能已被禁用，请管理员在 FrostAgent 后端启用此功能。"}`), nil
				}
				switch msg.Type {
				case "plain":
					if runtimescope.FromContext(ctx).Getenv(replyformat.EnvKey) != "true" {
						msg.Text = strings.Join(replyformat.Split(msg.Text, false), "")
						payload.Messages[i].Text = msg.Text
					}
					if strings.TrimSpace(msg.Text) == "" {
						return textResult(""), fmt.Errorf("messages[%d].text 不能为空", i)
					}
				case "mention_user":
					if strings.TrimSpace(msg.MentionUserID) == "" {
						return textResult(""), fmt.Errorf("messages[%d].mention_user_id 不能为空", i)
					}
				case "quote":
					if payload.SuppressAutoMention {
						return textResult(""), fmt.Errorf("mention_specific 不能与其他引用混用")
					}
					if strings.TrimSpace(msg.MessageID) == "" {
						return textResult(""), fmt.Errorf("messages[%d].message_id 不能为空", i)
					}
				case "mention_specific":
					messageID := strings.TrimSpace(msg.MessageID)
					if i != 0 || len(payload.Messages) < 2 || payload.Session != "" {
						return textResult(""), fmt.Errorf("mention_specific 必须位于消息链开头并附带回复内容，且不能指定 session")
					}
					if messageID == "" || run.SessionID == "" || run.CanReplyToMessage == nil || !run.CanReplyToMessage(messageID) {
						return textResult(""), fmt.Errorf("mention_specific 的 message_id 不在当前会话的可信消息上下文中")
					}
					payload.Messages[i] = Msg{Type: "quote", MessageID: messageID}
					payload.SuppressAutoMention = true
				case "image", "record", "video", "file":
					if strings.TrimSpace(msg.Path) == "" && strings.TrimSpace(msg.URL) == "" {
						return textResult(""), fmt.Errorf("messages[%d] 需要 path 或 url", i)
					}
				default:
					return textResult(""), fmt.Errorf("send_message 不支持 messages[%d].type=%s；QQ 扩展组件请使用 qq_extend_messages", i, msg.Type)
				}
			}
			if run.PlatformActionHook != nil && hasQQMedia(payload.Messages) {
				if run.Proactive || run.Mock || run.PlatformActionHook == nil || payload.Session != "" {
					return textResult(""), fmt.Errorf("QQ 富消息需要当前活动 OneBot 会话")
				}
				if err := ValidateQQMessages(payload.Messages, false); err != nil {
					return textResult(""), err
				}
				request, _ := json.Marshal(QQRequest{Action: "send", Arguments: QQArguments{Messages: payload.Messages}, SuppressAutoMention: payload.SuppressAutoMention})
				return core.ToolExecutionResult{Content: string(request), PlatformActionPayload: string(request)}, nil
			}

			normalized, err := json.Marshal(payload)
			if err != nil {
				return textResult(""), fmt.Errorf("send_message 参数序列化失败: %w", err)
			}
			return core.ToolExecutionResult{Content: string(normalized), MessagePayload: string(normalized)}, nil
		},
	}
}

// Msg is shared with QQ extensions; keep the public basic input boundary narrow.
func validateBasicMessageFields(raw string) error {
	var input struct {
		Messages      []map[string]json.RawMessage `json:"messages"`
		TargetGroupID json.RawMessage              `json:"target_group_id"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return err
	}
	if input.TargetGroupID != nil {
		return fmt.Errorf("target_group_id 请通过 qq_extend_messages 指定")
	}
	for i, message := range input.Messages {
		for field := range message {
			switch field {
			case "type", "text", "mention_user_id", "message_id", "path", "url", "is_sticker":
			default:
				return fmt.Errorf("send_message 不支持 messages[%d].%s；QQ 扩展字段请使用 qq_extend_messages", i, field)
			}
		}
	}
	return nil
}

func BuildOneBotMessage(toolMessages []Msg) ([]OneBotSegment, error) {
	var oneBotChain []OneBotSegment

	for _, Msg := range toolMessages {
		switch Msg.Type {
		case "plain":
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: "text",
				Data: map[string]interface{}{"text": Msg.Text},
			})

		case "mention_user":
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: "at",
				Data: map[string]interface{}{"qq": Msg.MentionUserID},
			})
		case "mention_all":
			oneBotChain = append(oneBotChain, OneBotSegment{Type: "at", Data: map[string]any{"qq": "all"}})
		case "face", "dice", "rps", "contact", "music", "share", "location", "mface":
			if err := validateQQComponent(Msg); err != nil {
				return nil, err
			}
			if Msg.Type == "mface" && Msg.Data == nil {
				return nil, fmt.Errorf("observed market sticker must be resolved by the active adapter")
			}
			data := map[string]any{}
			if Msg.Data != nil {
				b, _ := json.Marshal(Msg.Data)
				_ = json.Unmarshal(b, &data)
			}
			oneBotChain = append(oneBotChain, OneBotSegment{Type: Msg.Type, Data: data})

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
			if Msg.Name != "" {
				segData["name"] = Msg.Name
			}
			if Msg.Summary != "" {
				segData["summary"] = Msg.Summary
			}
			if Msg.Thumb != "" {
				thumb, err := buildOneBotMediaFile(MsgForThumbnail(Msg.Thumb))
				if err != nil {
					return nil, err
				}
				segData["thumb"] = thumb
			}
			if Msg.Type == "image" && Msg.IsSticker {
				segData["sub_type"] = 1
				segData["subType"] = 1
			}
			oneBotChain = append(oneBotChain, OneBotSegment{
				Type: Msg.Type,
				Data: segData,
			})
		default:
			return nil, fmt.Errorf("unsupported message component %q", Msg.Type)
		}
	}

	return oneBotChain, nil
}

func MsgForThumbnail(source string) Msg {
	if webURL(source) || strings.HasPrefix(source, "base64://") {
		return Msg{Type: "image", URL: source}
	}
	return Msg{Type: "image", Path: source}
}

// NeedsQQDelivery selects the native extension path without changing existing
// ordinary text/mention delivery and its proactive reply contract.
func NeedsQQDelivery(messages []Msg) bool {
	for _, m := range messages {
		if m.Data != nil || m.Name != "" || m.FolderID != "" || m.Thumb != "" || m.Summary != "" || m.Transfer != "" {
			return true
		}
		switch m.Type {
		case "face", "dice", "rps", "contact", "music", "share", "location", "mention_all", "mface":
			return true
		}
	}
	return false
}

func hasQQMedia(messages []Msg) bool {
	for _, m := range messages {
		switch m.Type {
		case "image", "record", "video", "file":
			return true
		}
	}
	return false
}

func buildOneBotMediaFile(msg Msg) (string, error) {
	if msg.Path != "" {
		if msg.Type == "image" && msg.IsSticker {
			content, err := os.ReadFile(msg.Path)
			if err != nil {
				return "", fmt.Errorf("读取本地媒体文件 %q 失败: %w", msg.Path, err)
			}
			if len(content) == 0 {
				return "", fmt.Errorf("本地媒体文件 %q 为空", msg.Path)
			}
			return "base64://" + base64.StdEncoding.EncodeToString(content), nil
		}

		if err := validateLocalMediaFile(msg.Path); err != nil {
			return "", err
		}
		return fmt.Sprintf("file://%s", msg.Path), nil
	}
	return msg.URL, nil
}

func validateLocalMediaFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("读取本地媒体文件 %q 失败: %w", path, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("本地媒体文件 %q 是目录", path)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("本地媒体文件 %q 为空", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取本地媒体文件 %q 失败: %w", path, err)
	}
	defer f.Close()

	var probe [1]byte
	if _, err := f.Read(probe[:]); err != nil {
		return fmt.Errorf("读取本地媒体文件 %q 失败: %w", path, err)
	}
	return nil
}
