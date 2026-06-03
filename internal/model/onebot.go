package model

import "encoding/json"

type OneBotEvent struct {
	SelfID        int64           `json:"self_id"`
	PostType      string          `json:"post_type"`
	MetaEventType string          `json:"meta_event_type,omitempty"`
	MessageType   string          `json:"message_type,omitempty"`
	GroupID       int64           `json:"group_id,omitempty"`
	UserID        int64           `json:"user_id,omitempty"`
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
