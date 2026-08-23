package onebot

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	replyLookupTimeout      = time.Second
	defaultActionACKTimeout = 10 * time.Second
)

func actionACKTimeout() time.Duration {
	if s := strings.TrimSpace(os.Getenv("ONEBOT_ACTION_TIMEOUT")); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return defaultActionACKTimeout
}

type oneBotAPIResponse struct {
	PostType string          `json:"post_type"`
	Status   string          `json:"status"`
	RetCode  int             `json:"retcode"`
	Data     json.RawMessage `json:"data"`
	Echo     json.RawMessage `json:"echo"`
	Message  string          `json:"message"`
	Wording  string          `json:"wording"`
}

// SendActionAndWait sends a OneBot action and waits for the matching ACK response (via echo).
// It distinguishes between websocket write failures, platform ACK timeout, and platform errors (retcode != 0).
func (c *wsConnection) SendActionAndWait(action model.OneBotAction, timeout time.Duration) (oneBotAPIResponse, error) {
	if c == nil || c.conn == nil {
		return oneBotAPIResponse{}, fmt.Errorf("websocket connection is nil")
	}
	if timeout <= 0 {
		timeout = defaultActionACKTimeout
	}

	echo, responseChannel := c.registerActionRequest(action.Action, action.Echo)
	action.Echo = echo

	actionBytes, err := json.Marshal(action)
	if err != nil {
		c.clearMessageRequest(echo, responseChannel)
		return oneBotAPIResponse{}, fmt.Errorf("marshal action failed: %w", err)
	}

	if err := c.WriteMessage(websocket.TextMessage, actionBytes); err != nil {
		c.clearMessageRequest(echo, responseChannel)
		return oneBotAPIResponse{}, fmt.Errorf("websocket write failed: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case response := <-responseChannel:
		if response.RetCode != 0 || (response.Status != "" && response.Status != "ok") {
			detail := strings.TrimSpace(response.Wording)
			if detail == "" {
				detail = strings.TrimSpace(response.Message)
			}
			if detail == "" {
				detail = fmt.Sprintf("retcode=%d", response.RetCode)
			}
			return response, fmt.Errorf("onebot api error: retcode=%d %s", response.RetCode, detail)
		}
		return response, nil
	case <-timer.C:
		c.clearMessageRequest(echo, responseChannel)
		return oneBotAPIResponse{}, fmt.Errorf("action %s timeout after %v (echo=%s)", action.Action, timeout, echo)
	}
}

func (c *wsConnection) registerActionRequest(actionName, existingEcho string) (string, chan oneBotAPIResponse) {
	c.messageMu.Lock()
	defer c.messageMu.Unlock()
	if c.pendingMessage == nil {
		c.pendingMessage = make(map[string]chan oneBotAPIResponse)
	}
	c.nextMessageEcho++
	var echo string
	if existingEcho != "" && !strings.HasPrefix(existingEcho, "echo_") {
		echo = existingEcho
	} else {
		echo = fmt.Sprintf("frost_action_%s_%d", actionName, c.nextMessageEcho)
	}
	responseChannel := make(chan oneBotAPIResponse, 1)
	c.pendingMessage[echo] = responseChannel
	return echo, responseChannel
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

// lookupReplyContext resolves the first reply segment through OneBot get_msg.
// It waits at most one second and always degrades to an empty context so stale
// or unsupported history cannot block the current event's normal wake signal.
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
