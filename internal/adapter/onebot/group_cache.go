package onebot

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
)

const (
	groupInfoCacheTTL   = time.Hour
	groupInfoFailureTTL = time.Minute
	groupInfoPendingTTL = 15 * time.Second
	maxContextNameRunes = 128
)

type cachedGroupInfo struct {
	Name      string
	ExpiresAt time.Time
}

type pendingGroupInfo struct {
	GroupID     int64
	RequestedAt time.Time
}

// groupName returns a fresh cached group name or starts a non-blocking
// get_group_info request. A cache miss never delays the current chat turn.
func (c *wsConnection) groupName(groupID int64) string {
	if c == nil || groupID == 0 {
		return ""
	}

	now := time.Now()
	c.groupMu.Lock()
	if cached, ok := c.groupCache[groupID]; ok {
		if now.Before(cached.ExpiresAt) {
			c.groupMu.Unlock()
			return cached.Name
		}
		delete(c.groupCache, groupID)
	}

	if echo, ok := c.pendingGroupByID[groupID]; ok {
		pending, exists := c.pendingGroupByEcho[echo]
		if exists && now.Sub(pending.RequestedAt) < groupInfoPendingTTL {
			c.groupMu.Unlock()
			return ""
		}
		delete(c.pendingGroupByID, groupID)
		delete(c.pendingGroupByEcho, echo)
	}

	c.nextGroupEcho++
	echo := fmt.Sprintf("frost_group_info_%d_%d", groupID, c.nextGroupEcho)
	c.pendingGroupByID[groupID] = echo
	c.pendingGroupByEcho[echo] = pendingGroupInfo{
		GroupID:     groupID,
		RequestedAt: now,
	}
	c.groupMu.Unlock()

	actionBytes, err := json.Marshal(model.OneBotAction{
		Action: "get_group_info",
		Params: map[string]interface{}{
			"group_id": groupID,
			"no_cache": false,
		},
		Echo: echo,
	})
	if err != nil {
		c.clearPendingGroupInfo(echo)
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("构造群信息查询失败: %v", err))
		return ""
	}
	if err := c.WriteMessage(websocket.TextMessage, actionBytes); err != nil {
		c.clearPendingGroupInfo(echo)
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("发送群信息查询失败 group=%d: %v", groupID, err))
	}
	return ""
}

// handleAPIResponse consumes OneBot action responses before the event decoder,
// dispatching awaited lookups and logging failures from fire-and-forget actions.
func (c *wsConnection) handleAPIResponse(raw []byte) bool {
	var response oneBotAPIResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.PostType != "" ||
		len(response.Echo) == 0 || string(response.Echo) == "null" {
		return false
	}

	var echo string
	if err := json.Unmarshal(response.Echo, &echo); err != nil || echo == "" {
		return true
	}
	if c.deliverMessageResponse(echo, response) {
		return true
	}

	c.groupMu.Lock()
	pending, ok := c.pendingGroupByEcho[echo]
	if !ok {
		c.groupMu.Unlock()
		logOneBotActionFailure(echo, response)
		return true
	}
	delete(c.pendingGroupByEcho, echo)
	if c.pendingGroupByID[pending.GroupID] == echo {
		delete(c.pendingGroupByID, pending.GroupID)
	}

	name := ""
	success := response.RetCode == 0 && (response.Status == "" || response.Status == "ok")
	if success {
		var data struct {
			GroupID   int64  `json:"group_id"`
			GroupName string `json:"group_name"`
		}
		if err := json.Unmarshal(response.Data, &data); err != nil ||
			(data.GroupID != 0 && data.GroupID != pending.GroupID) {
			success = false
		} else {
			name = sanitizeContextName(data.GroupName)
			success = name != ""
		}
	}

	ttl := groupInfoCacheTTL
	if !success {
		ttl = groupInfoFailureTTL
		name = ""
	}
	c.groupCache[pending.GroupID] = cachedGroupInfo{
		Name:      name,
		ExpiresAt: time.Now().Add(ttl),
	}
	c.groupMu.Unlock()

	if success {
		logs.Info(
			logs.WEBSOCKET,
			fmt.Sprintf("群名称缓存已更新: group=%d name=%q", pending.GroupID, name),
		)
	} else {
		logs.Warn(
			logs.WEBSOCKET,
			fmt.Sprintf("群信息查询失败，短暂负缓存: group=%d retcode=%d", pending.GroupID, response.RetCode),
		)
	}
	return true
}

func logOneBotActionFailure(echo string, response oneBotAPIResponse) {
	if response.RetCode == 0 && (response.Status == "" || response.Status == "ok") {
		return
	}
	detail := strings.TrimSpace(response.Wording)
	if detail == "" {
		detail = strings.TrimSpace(response.Message)
	}
	logs.Error(
		logs.WEBSOCKET,
		fmt.Sprintf(
			"OneBot action 失败: echo=%s status=%s retcode=%d detail=%q",
			echo,
			response.Status,
			response.RetCode,
			detail,
		),
	)
}

func (c *wsConnection) clearPendingGroupInfo(echo string) {
	c.groupMu.Lock()
	defer c.groupMu.Unlock()
	pending, ok := c.pendingGroupByEcho[echo]
	if !ok {
		return
	}
	delete(c.pendingGroupByEcho, echo)
	if c.pendingGroupByID[pending.GroupID] == echo {
		delete(c.pendingGroupByID, pending.GroupID)
	}
}

func senderContext(event model.OneBotEvent) map[string]interface{} {
	if event.Sender == nil {
		return nil
	}

	userID := event.UserID
	if userID == 0 {
		userID = event.Sender.UserID
	}
	context := make(map[string]interface{})
	if userID != 0 {
		context["user_id"] = userID
	}

	nickname := sanitizeContextName(event.Sender.Nickname)
	card := sanitizeContextName(event.Sender.Card)
	if nickname != "" {
		context["nickname"] = nickname
	}
	if card != "" && card != nickname {
		context["card"] = card
	}
	return context
}

func senderDisplayName(event model.OneBotEvent) string {
	if event.Sender == nil {
		return "未提供"
	}
	nickname := sanitizeContextName(event.Sender.Nickname)
	card := sanitizeContextName(event.Sender.Card)
	if card != "" {
		if nickname != "" && nickname != card {
			return fmt.Sprintf("%s（%s）", card, nickname)
		}
		return card
	}
	if nickname != "" {
		return nickname
	}
	return "未提供"
}

func sanitizeContextName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	runes := []rune(value)
	if len(runes) > maxContextNameRunes {
		value = string(runes[:maxContextNameRunes])
	}
	return strings.TrimSpace(value)
}
