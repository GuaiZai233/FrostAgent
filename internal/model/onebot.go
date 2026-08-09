package model

import "encoding/json"

// OneBotSender contains the best-effort sender metadata attached to OneBot v11
// message events. Implementations may omit fields or return cached values.
type OneBotSender struct {
	UserID   int64  `json:"user_id,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Card     string `json:"card,omitempty"`
	Sex      string `json:"sex,omitempty"`
	Age      int32  `json:"age,omitempty"`
	Area     string `json:"area,omitempty"`
	Level    string `json:"level,omitempty"`
	Role     string `json:"role,omitempty"`
	Title    string `json:"title,omitempty"`
}

type OneBotEvent struct {
	SelfID        int64           `json:"self_id"`
	PostType      string          `json:"post_type"`
	MetaEventType string          `json:"meta_event_type,omitempty"`
	MessageType   string          `json:"message_type,omitempty"`
	GroupID       int64           `json:"group_id,omitempty"`
	UserID        int64           `json:"user_id,omitempty"`
	Sender        *OneBotSender   `json:"sender,omitempty"`
	Message       json.RawMessage `json:"message,omitempty"`
	// Messages is an optional extension used by FrostAgent adapters to pass
	// continuous user message contexts in one event. Each item may be a OneBot
	// message segment array or a plain string.
	Messages  json.RawMessage `json:"messages,omitempty"`
	MessageID int32           `json:"message_id,omitempty"`
}

type OneBotAction struct {
	Action string      `json:"action"`
	Params interface{} `json:"params,omitempty"`
	Echo   string      `json:"echo,omitempty"`
}
