package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/runtimescope"
	"encoding/json"
	"strings"
)

// Event 表示 AstrBot 插件发送给 FrostAgent 的入站事件。
type Event struct {
	Type        string            `json:"type"`                   // "event" 或 "heartbeat"
	EventType   string            `json:"event_type,omitempty"`   // "message", "heartbeat", "notice"
	MessageID   string            `json:"message_id,omitempty"`   // 消息唯一 ID
	SessionID   string            `json:"session_id,omitempty"`   // 会话 ID (如 "astrbot:group:123" 或 "astrbot:private:456")
	Platform    string            `json:"platform,omitempty"`     // 平台标识 (如 "astrbot", "qq", "telegram", "wechat")
	MessageType string            `json:"message_type,omitempty"` // "group", "private"
	UserID      string            `json:"user_id,omitempty"`      // 用户 ID
	SenderName  string            `json:"sender_name,omitempty"`  // 发送者昵称
	SenderCard  string            `json:"sender_card,omitempty"`  // 发送者群名片/备注
	GroupID     string            `json:"group_id,omitempty"`     // 群 ID
	GroupName   string            `json:"group_name,omitempty"`   // 群名称
	Content     string            `json:"content,omitempty"`      // 文本内容
	Messages    []string          `json:"messages,omitempty"`     // 连续上下文消息 (可选)
	Attachments []core.Attachment `json:"attachments,omitempty"`  // 多媒体附件
	IsWake      bool              `json:"is_wake,omitempty"`      // 是否触发唤醒 (At, 唤醒词, 引用等)
	IsAt        bool              `json:"is_at,omitempty"`        // 是否 @ 了机器人
	Timestamp   int64             `json:"timestamp,omitempty"`    // 时间戳 (秒)
	Metadata    map[string]any    `json:"metadata,omitempty"`     // 扩展元数据
}

// ActionMessage represents one ordered component in an outbound AstrBot message chain.
type ActionMessage struct {
	Type          string `json:"type"`
	Text          string `json:"text,omitempty"`
	MentionUserID string `json:"mention_user_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	Path          string `json:"path,omitempty"`
	URL           string `json:"url,omitempty"`
	IsSticker     bool   `json:"is_sticker,omitempty"`
}

// Action 表示 FrostAgent 发送给 AstrBot 插件的出站动作。
type Action struct {
	Type           string            `json:"type"`                      // 默认为 "action"
	Action         string            `json:"action"`                    // "send_message"
	SessionID      string            `json:"session_id,omitempty"`      // 目标会话 ID
	TargetID       string            `json:"target_id,omitempty"`       // 目标群 ID 或用户 ID
	MessageType    string            `json:"message_type,omitempty"`    // "group" 或 "private"
	GroupID        string            `json:"group_id,omitempty"`        // 群 ID
	UserID         string            `json:"user_id,omitempty"`         // 用户 ID
	Content        string            `json:"content,omitempty"`         // 回复文本内容
	Messages       []ActionMessage   `json:"messages,omitempty"`        // 有序消息链（plain、mention_user、媒体等）
	Attachments    []core.Attachment `json:"attachments,omitempty"`     // 附件列表 (图片等)
	IsIntermediate bool              `json:"is_intermediate,omitempty"` // 是否为工具调用产生的中间消息 (sendHook)
	Echo           string            `json:"echo,omitempty"`            // 回显标识
	ReplyMessageID string            `json:"-"`                         // 当前回复对应的入站消息 ID，仅用于出站规范化
}

// MarshalJSON preserves the legacy process-env normalization path for callers
// that serialize Action directly. Instance-bound WebSocket delivery bypasses
// this method after normalizing with the connection's runtime scope.
func (a Action) MarshalJSON() ([]byte, error) {
	type actionAlias Action
	normalized := a.withConfiguredGroupMention().withConfiguredGroupReply()
	return json.Marshal(actionAlias(normalized))
}

func (a Action) containsSticker() bool {
	for _, message := range a.Messages {
		if message.IsSticker {
			return true
		}
	}
	for _, attachment := range a.Attachments {
		if attachment.Type == core.AttachmentTypeImage && attachment.SubType == 1 {
			return true
		}
	}
	return false
}

func (a Action) withConfiguredGroupMention() Action {
	return a.withConfiguredGroupMentionScope(nil)
}

func (a Action) withConfiguredGroupMentionScope(scope *runtimescope.Scope) Action {
	if a.Action != "send_message" || a.MessageType != "group" || a.containsSticker() || scope.Getenv("ENABLE_AT_IN_GROUP_MSG") != "true" {
		return a
	}

	userID := strings.TrimSpace(a.UserID)
	if userID == "" {
		return a
	}

	for _, message := range a.Messages {
		if message.Type == "mention_user" && strings.TrimSpace(message.MentionUserID) == userID {
			return a
		}
	}

	mention := ActionMessage{Type: "mention_user", MentionUserID: userID}
	if len(a.Messages) > 0 {
		messages := make([]ActionMessage, 0, len(a.Messages)+1)
		insertAt := 0
		for insertAt < len(a.Messages) && isQuoteMessage(a.Messages[insertAt]) {
			insertAt++
		}
		messages = append(messages, a.Messages[:insertAt]...)
		messages = append(messages, mention)
		messages = append(messages, a.Messages[insertAt:]...)
		a.Messages = messages
		return a
	}

	// Plain direct replies historically use Content instead of Messages. Promote
	// them to an ordered message chain so the AstrBot plugin can emit a native At
	// component while retaining Content as a compatibility fallback for consumers.
	if strings.TrimSpace(a.Content) != "" {
		a.Messages = []ActionMessage{
			mention,
			{Type: "plain", Text: a.Content},
		}
	}
	return a
}

func (a Action) withConfiguredGroupReply() Action {
	return a.withConfiguredGroupReplyScope(nil)
}

func (a Action) withConfiguredGroupReplyScope(scope *runtimescope.Scope) Action {
	if a.Action != "send_message" || a.MessageType != "group" || a.containsSticker() || scope.Getenv("ENABLE_REPLY_IN_GROUP_MSG") != "true" {
		return a
	}

	replyMessageID := strings.TrimSpace(a.ReplyMessageID)
	if replyMessageID == "" {
		return a
	}
	for _, message := range a.Messages {
		if isQuoteMessage(message) {
			return a
		}
	}

	quote := ActionMessage{Type: "quote", MessageID: replyMessageID}
	if len(a.Messages) > 0 {
		messages := make([]ActionMessage, 0, len(a.Messages)+1)
		messages = append(messages, quote)
		messages = append(messages, a.Messages...)
		a.Messages = messages
		return a
	}

	if strings.TrimSpace(a.Content) != "" {
		a.Messages = []ActionMessage{
			quote,
			{Type: "plain", Text: a.Content},
		}
	}
	return a
}

func isQuoteMessage(message ActionMessage) bool {
	return message.Type == "quote" || message.Type == "reply"
}
