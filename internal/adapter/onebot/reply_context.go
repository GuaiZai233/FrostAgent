package onebot

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const replyLookupTimeout = time.Second

type oneBotAPIResponse struct {
	PostType string          `json:"post_type"`
	Status   string          `json:"status"`
	RetCode  int             `json:"retcode"`
	Data     json.RawMessage `json:"data"`
	Echo     json.RawMessage `json:"echo"`
	Message  string          `json:"message"`
	Wording  string          `json:"wording"`
}

type resolvedReplyContext struct {
	Prompt      string
	MentionsBot bool
}

type oneBotMessageData struct {
	Time        int64               `json:"time"`
	MessageType string              `json:"message_type"`
	MessageID   int64               `json:"message_id"`
	RealID      int64               `json:"real_id"`
	GroupID     int64               `json:"group_id"`
	UserID      int64               `json:"user_id"`
	Sender      *model.OneBotSender `json:"sender"`
	Message     json.RawMessage     `json:"message"`
}

func (c *wsConnection) lookupReplyContext(event model.OneBotEvent) resolvedReplyContext {
	messageID, ok := replyMessageID(event)
	if !ok || c == nil {
		return resolvedReplyContext{}
	}

	echo, responseChannel := c.registerMessageRequest(messageID)
	action, err := json.Marshal(model.OneBotAction{
		Action: "get_msg",
		Params: map[string]interface{}{"message_id": messageID},
		Echo:   echo,
	})
	if err != nil {
		c.clearMessageRequest(echo, responseChannel)
		logReplyLookupFailure(messageID, fmt.Sprintf("构造查询失败: %v", err))
		return resolvedReplyContext{}
	}
	if err := c.WriteMessage(websocket.TextMessage, action); err != nil {
		c.clearMessageRequest(echo, responseChannel)
		logReplyLookupFailure(messageID, fmt.Sprintf("发送查询失败: %v", err))
		return resolvedReplyContext{}
	}

	timer := time.NewTimer(replyLookupTimeout)
	defer timer.Stop()
	select {
	case response := <-responseChannel:
		return resolveReplyResponse(messageID, response)
	case <-timer.C:
		c.clearMessageRequest(echo, responseChannel)
		logReplyLookupFailure(messageID, "查询超时")
		return resolvedReplyContext{}
	}
}

func (c *wsConnection) registerMessageRequest(messageID int64) (string, chan oneBotAPIResponse) {
	c.messageMu.Lock()
	defer c.messageMu.Unlock()
	if c.pendingMessage == nil {
		c.pendingMessage = make(map[string]chan oneBotAPIResponse)
	}
	c.nextMessageEcho++
	echo := fmt.Sprintf("frost_get_msg_%d_%d", messageID, c.nextMessageEcho)
	responseChannel := make(chan oneBotAPIResponse, 1)
	c.pendingMessage[echo] = responseChannel
	return echo, responseChannel
}

func (c *wsConnection) clearMessageRequest(echo string, expected chan oneBotAPIResponse) {
	c.messageMu.Lock()
	defer c.messageMu.Unlock()
	if c.pendingMessage[echo] == expected {
		delete(c.pendingMessage, echo)
	}
}

func (c *wsConnection) deliverMessageResponse(echo string, response oneBotAPIResponse) bool {
	c.messageMu.Lock()
	responseChannel, ok := c.pendingMessage[echo]
	if ok {
		delete(c.pendingMessage, echo)
	}
	c.messageMu.Unlock()
	if !ok {
		return false
	}
	responseChannel <- response
	return true
}

func resolveReplyResponse(messageID int64, response oneBotAPIResponse) resolvedReplyContext {
	if response.RetCode != 0 || (response.Status != "" && response.Status != "ok") {
		detail := strings.TrimSpace(response.Wording)
		if detail == "" {
			detail = strings.TrimSpace(response.Message)
		}
		logReplyLookupFailure(messageID, fmt.Sprintf("retcode=%d %s", response.RetCode, detail))
		return resolvedReplyContext{}
	}

	var data oneBotMessageData
	if err := json.Unmarshal(response.Data, &data); err != nil {
		logReplyLookupFailure(messageID, fmt.Sprintf("解析响应失败: %v", err))
		return resolvedReplyContext{}
	}
	if data.MessageID != 0 && data.MessageID != messageID {
		logReplyLookupFailure(messageID, fmt.Sprintf("响应消息 ID 不匹配: %d", data.MessageID))
		return resolvedReplyContext{}
	}

	visibleText := extractUserText(ParseMessageSegments(data.Message), data.Message)
	context := map[string]interface{}{
		"message_id": messageID,
		"message":    visibleText,
	}
	if data.Time != 0 {
		context["time"] = data.Time
	}
	if data.MessageType != "" {
		context["message_type"] = data.MessageType
	}
	if data.GroupID != 0 {
		context["group_id"] = data.GroupID
	}
	if sender := senderContext(model.OneBotEvent{UserID: data.UserID, Sender: data.Sender}); len(sender) > 0 {
		context["sender"] = sender
	}
	prompt, err := json.Marshal(context)
	if err != nil {
		logReplyLookupFailure(messageID, fmt.Sprintf("构造引用上下文失败: %v", err))
		return resolvedReplyContext{}
	}

	return resolvedReplyContext{
		Prompt:      string(prompt),
		MentionsBot: rawMessageMentionsBot(data.Message, configuredBotNames()),
	}
}

func replyMessageID(event model.OneBotEvent) (int64, bool) {
	for _, raw := range EventRawMessages(event) {
		for _, segment := range ParseMessageSegments(raw) {
			if segment.Type != "reply" {
				continue
			}
			if messageID, ok := numericMessageID(segment.Data["id"]); ok {
				return messageID, true
			}
		}
	}
	return 0, false
}

func numericMessageID(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case string:
		messageID, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return messageID, err == nil
	case float64:
		messageID := int64(typed)
		return messageID, float64(messageID) == typed
	case json.Number:
		messageID, err := typed.Int64()
		return messageID, err == nil
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}

func logReplyLookupFailure(messageID int64, detail string) {
	// Keep failures non-fatal: the current message can still wake the bot via
	// an explicit at/name signal even when historical content is unavailable.
	logs.Warn(logs.WEBSOCKET, fmt.Sprintf("引用消息查询失败 message=%d: %s", messageID, detail))
}
