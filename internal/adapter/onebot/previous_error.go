package onebot

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

// takePreviousError snapshots the preceding message before the current message
// invalidates it, without waiting for a potentially blocked socket write.
func (c *wsConnection) takePreviousError(event model.OneBotEvent) string {
	if c == nil || c.mock || (event.PostType != "message" && event.PostType != "message_sent") ||
		(event.MessageType != "private" && event.MessageType != "group") {
		return ""
	}
	if event.PostType == "message_sent" && event.MessageType == "private" {
		data := oneBotMessageData{UserID: event.UserID, Sender: event.Sender}
		if event.TargetID != 0 {
			data.TargetID = event.TargetID
		}
		peer, known := privateMessagePeer(event.SelfID, data)
		if !known || peer <= 0 {
			return ""
		}
		event.UserID = peer
	}
	c.previousErrorMu.Lock()
	defer c.previousErrorMu.Unlock()
	key := historyKey(event)
	id := c.previousErrors[key]
	if event.PostType == "message_sent" && id != "" && outgoingErrorUUID(event.Message) == id {
		return "" // The platform is echoing the current error, not a new message.
	}
	delete(c.previousErrors, key)
	if event.PostType == "message_sent" {
		return "" // Echoes may invalidate state, but never revive an older error.
	}
	return id
}

// rememberOutgoingError runs under writeMu, immediately after a successful write.
// ACKs and optional chat history collection must not change message ordering.
func (c *wsConnection) rememberOutgoingError(data []byte) {
	if c.mock {
		return
	}
	var action struct {
		Action string `json:"action"`
		Params struct {
			UserID  json.RawMessage `json:"user_id"`
			GroupID json.RawMessage `json:"group_id"`
			Message json.RawMessage `json:"message"`
		} `json:"params"`
	}
	if json.Unmarshal(data, &action) != nil {
		return
	}
	switch action.Action {
	case "send_msg", "send_group_msg", "send_private_msg", "send_group_forward_msg", "send_private_forward_msg", "send_forward_msg", "forward_group_single_msg", "forward_friend_single_msg":
	default:
		return
	}
	kind, target := "private", historyRawID(action.Params.UserID)
	if group := historyRawID(action.Params.GroupID); group != "" && group != "0" {
		kind, target = "group", group
	}
	id, err := strconv.ParseInt(target, 10, 64)
	if err != nil || id <= 0 {
		return
	}
	c.previousErrorMu.Lock()
	defer c.previousErrorMu.Unlock()
	key := kind + ":" + strconv.FormatInt(id, 10)
	uuid := outgoingErrorUUID(action.Params.Message)
	if uuid == "" {
		delete(c.previousErrors, key)
		return
	}
	if c.previousErrors == nil {
		c.previousErrors = make(map[string]string)
	}
	// Keep transient transport state bounded even for many inactive conversations.
	if _, exists := c.previousErrors[key]; !exists && len(c.previousErrors) >= maxTrustedMessageSessions {
		for oldKey := range c.previousErrors {
			delete(c.previousErrors, oldKey)
			break
		}
	}
	c.previousErrors[key] = uuid
}

func outgoingErrorUUID(raw json.RawMessage) string {
	var text strings.Builder
	for _, segment := range ParseMessageSegments(raw) {
		switch segment.Type {
		case "text":
			value, _ := segment.Data["text"].(string)
			text.WriteString(value)
		case "at", "reply":
			// Group reply decorations are not part of the error text.
		default:
			return ""
		}
	}
	message := strings.TrimSpace(text.String())
	if !strings.HasPrefix(message, "[FrostAgent 错误] ") || !strings.HasSuffix(message, ")") {
		return ""
	}
	_, reference, found := strings.Cut(message, " (Ref_ID: ")
	if !found {
		return ""
	}
	uuid, _ := logs.NormalizeErrorUUID(strings.TrimSuffix(reference, ")"))
	return uuid
}
