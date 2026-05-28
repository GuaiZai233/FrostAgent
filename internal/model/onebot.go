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
}

type OneBotAction struct {
	Action string      `json:"action"`
	Params interface{} `json:"params,omitempty"`
	Echo   string      `json:"echo,omitempty"`
}
