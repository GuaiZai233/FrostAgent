package astrbot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: checkWebSocketOrigin,
}

var allowedOrigins []string

func init() {
	env := os.Getenv("WS_ALLOWED_ORIGINS")
	if env != "" {
		for _, o := range strings.Split(env, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				allowedOrigins = append(allowedOrigins, trimmed)
			}
		}
	}
}

func checkWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot: 拒绝 WebSocket 连接：非法 Origin %q", origin))
		return false
	}

	if strings.EqualFold(originURL.Host, r.Host) {
		return true
	}

	for _, allowed := range allowedOrigins {
		if strings.EqualFold(allowed, origin) || strings.EqualFold(allowed, originURL.Host) {
			return true
		}
	}

	logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot: 拒绝 WebSocket 连接：Origin %q 不在允许列表", origin))
	return false
}

type wsConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func newWSConn(conn *websocket.Conn) *wsConn {
	return &wsConn{conn: conn}
}

func (c *wsConn) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("connection closed")
	}
	return c.conn.WriteMessage(messageType, data)
}

func (c *wsConn) WriteJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return errors.New("connection closed")
	}
	return c.conn.WriteJSON(v)
}

func (c *wsConn) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func sessionKey(event Event) string {
	if event.SessionID != "" {
		return event.SessionID
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	if event.MessageType == "group" {
		return fmt.Sprintf("%s:group:%s", platform, event.GroupID)
	}
	return fmt.Sprintf("%s:private:%s", platform, event.UserID)
}

func senderDisplayName(event Event) string {
	if event.SenderCard != "" {
		return event.SenderCard
	}
	if event.SenderName != "" {
		return event.SenderName
	}
	if event.UserID != "" {
		return event.UserID
	}
	return "unknown"
}

func captureGroupCompactMessage(event Event, engine *llm.Engine) {
	if engine == nil || engine.GroupCompactor == nil || event.GroupID == "" {
		return
	}
	visibleText := strings.TrimSpace(event.Content)
	if visibleText == "" {
		return
	}
	session := engine.SessionManager.GetOrCreate(sessionKey(event))
	session.AppendGroupCompactMessage(
		formatGroupSpeakerMessage(event, visibleText),
		engine.GroupCompactor.BufferSize(),
	)
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	owner, _ := memory.OwnerForPlatformGroup(platform, event.GroupID)
	engine.GroupCompactor.Trigger(session, owner)
}

func formatGroupSpeakerMessage(event Event, text string) string {
	return fmt.Sprintf("%s (%s): %s", senderDisplayName(event), event.UserID, strings.TrimSpace(text))
}

func processEvent(conn *wsConn, event Event, engine *llm.Engine, turn *llm.SessionTurn) {
	if turn != nil {
		turn.Wait()
		defer turn.Done()
	}

	if event.Type != "event" && event.Type != "" {
		return
	}
	if event.EventType != "" && event.EventType != "message" {
		return
	}

	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}

	logs.Info(
		logs.WEBSOCKET,
		fmt.Sprintf(
			"AstrBot 收到 [%s] %s 消息 (ID:%s User:%s/%s Group:%s): %s",
			platform,
			event.MessageType,
			event.MessageID,
			event.UserID,
			senderDisplayName(event),
			event.GroupID,
			event.Content,
		),
	)

	reply(event, engine, conn)
}

func reply(event Event, engine *llm.Engine, conn *wsConn) {
	userText := event.Content

	// 处理图片等多模态内容描述
	var imageSegments []content.MessageSegment
	for _, att := range event.Attachments {
		if att.Type == core.AttachmentTypeImage && att.URL != "" {
			imageSegments = append(imageSegments, content.MessageSegment{
				Type: "image",
				Data: map[string]any{"url": att.URL},
			})
		}
	}

	if len(imageSegments) > 0 && engine != nil {
		// 计费检查 (视觉处理前检查)
		platform := event.Platform
		if platform == "" {
			platform = "astrbot"
		}
		if engine.BillingClient != nil && engine.BillingConfig.Enabled {
			bCtx, bCancel := context.WithTimeout(context.Background(), engine.BillingConfig.Timeout)
			bal, err := engine.BillingClient.Balance(bCtx, platform, event.UserID)
			bCancel()
			if err != nil {
				if errors.Is(err, billing.ErrInsufficientFunds) {
					logs.Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 用户 [%s] 雪花余额不足，拒绝视觉处理", event.UserID))
					sendDirectReply(event, conn, billing.FormatInsufficientFundsMessage(0))
					return
				}
				logs.Error(logs.SYSTEM, fmt.Sprintf("Alcyone 计费服务不可用 (fail-closed, vision): %v", err))
				sendDirectReply(event, conn, billing.FormatBillingUnavailableMessage())
				return
			}
			if bal != nil && bal.Exists && bal.BalanceMinor <= 0 {
				logs.Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 用户 [%s] 余额为 0，拒绝视觉处理", event.UserID))
				sendDirectReply(event, conn, billing.FormatInsufficientFundsMessage(0))
				return
			}
		}

		imageDesc := content.ProcessImage(imageSegments, engine.Provider, engine.BaseURL, engine.APIKey, engine.ModelName)
		if imageDesc != "" {
			userText = strings.TrimSpace(userText + " 【图片内容】：" + imageDesc)
		}
	}

	var session *llm.SessionContext
	runningSummary := ""
	if engine != nil && engine.SessionManager != nil {
		session = engine.SessionManager.GetOrCreate(sessionKey(event))
		if event.MessageType == "group" {
			runningSummary = session.GroupRunningSummary()
		}
	}

	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}

	var (
		owner     string
		ownerType memory.OwnerType
	)
	if event.MessageType == "group" {
		owner, ownerType = memory.OwnerForPlatformGroup(platform, event.GroupID)
	} else {
		owner, ownerType = memory.OwnerForPlatformPrivate(platform, event.UserID)
	}

	contextData := map[string]any{
		"sender_id":   event.UserID,
		"sender_name": senderDisplayName(event),
		"platform":    platform,
	}
	if event.MessageType == "group" {
		contextData["group_id"] = event.GroupID
		contextData["group_name"] = event.GroupName
	}
	contextBytes, _ := json.Marshal(contextData)

	prompt := fmt.Sprintf("User Message: %s", userText)
	if runningSummary != "" {
		prompt += fmt.Sprintf(
			"\n\n<group_running_summary>\n%s\n</group_running_summary>",
			runningSummary,
		)
	}
	prompt += fmt.Sprintf("\n\n<system_context>\n%s\n</system_context>", string(contextBytes))

	var (
		replyText   string
		receiptText string
	)

	if engine != nil && session != nil {
		var billingState *llm.BillingRunState
		if engine.BillingClient != nil && engine.BillingConfig.Enabled {
			taskID := fmt.Sprintf("%s_%s_%s", platform, event.UserID, event.MessageID)
			billingState = &llm.BillingRunState{
				Platform:      platform,
				ExternalID:    event.UserID,
				DisplayName:   senderDisplayName(event),
				TaskID:        taskID,
				BillingActive: true,
			}
		}

		session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: prompt})
		messages := session.Snapshot()

		targetID := event.UserID
		if event.MessageType == "group" {
			targetID = event.GroupID
		}

		sendHook := func(toolResultJSON string) {
			var toolOutput struct {
				Messages []tools.Msg `json:"messages"`
			}
			if err := json.Unmarshal([]byte(toolResultJSON), &toolOutput); err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot SendHook: 解析 send_message 结果失败: %v", err))
				return
			}
			for _, m := range toolOutput.Messages {
				var attachments []core.Attachment
				text := ""
				switch m.Type {
				case "plain":
					text = m.Text
				case "image":
					url := m.URL
					if url == "" {
						url = m.Path
					}
					attachments = append(attachments, core.Attachment{
						Type: core.AttachmentTypeImage,
						URL:  url,
					})
				case "record":
					url := m.URL
					if url == "" {
						url = m.Path
					}
					attachments = append(attachments, core.Attachment{
						Type: core.AttachmentTypeAudio,
						URL:  url,
					})
				case "video":
					url := m.URL
					if url == "" {
						url = m.Path
					}
					attachments = append(attachments, core.Attachment{
						Type: core.AttachmentTypeVideo,
						URL:  url,
					})
				default:
					text = m.Text
				}
				action := Action{
					Type:           "action",
					Action:         "send_message",
					SessionID:      sessionKey(event),
					TargetID:       targetID,
					MessageType:    event.MessageType,
					GroupID:        event.GroupID,
					UserID:         event.UserID,
					Content:        text,
					Attachments:    attachments,
					IsIntermediate: true,
					Echo:           fmt.Sprintf("hook_%s", event.MessageID),
				}
				if err := conn.WriteJSON(action); err != nil {
					logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot SendHook: 发送消息失败: %v", err))
				}
			}
		}

		runResult := engine.RunMessagesWithContext(messages, llm.RunContext{
			Owner:     owner,
			OwnerType: ownerType,
			SendHook:  sendHook,
			Billing:   billingState,
		})
		replyText = runResult.Content

		if billingState != nil && billingState.BillingActive {
			if runResult.Error != nil && billingState.IterationsBilled == 0 {
				session.TrimHistory(len(session.Snapshot()) - 1)
				sendDirectReply(event, conn, runResult.Content)
				return
			}
			receiptText = billing.FormatReceipt(
				billingState.TotalBilledMinor,
				runResult.Usage.PromptTokens,
				runResult.Usage.CompletionTokens,
				billingState.LastBalanceMinor,
				billingState.WelcomeGranted,
			)
		}

		if runResult.Silent {
			session.AddMessage(core.ChatMessage{
				Role:    core.RoleAssistant,
				Content: llm.AssistantSilentMarker,
			})
			engine.TrimSession(session)
			logs.Info(logs.SYSTEM, fmt.Sprintf("AstrBot: 本轮保持沉默: session=%s", sessionKey(event)))
			return
		}

		session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: replyText})
		engine.TrimSession(session)

		if runResult.MemoryWritten {
			logs.Info(logs.SYSTEM, "AstrBot: 本轮已通过 memory.write 处理记忆，跳过自动提取累计")
		} else if strings.TrimSpace(userText) != "" && strings.TrimSpace(replyText) != "" {
			pendingUserText := userText
			if event.MessageType == "group" {
				pendingUserText = formatGroupSpeakerMessage(event, userText)
			}
			engine.EnqueueExtractionTurn(session, []memory.PendingExtractionItem{
				{
					Owner:     owner,
					OwnerType: ownerType,
					Message:   core.ChatMessage{Role: core.RoleUser, Content: pendingUserText},
				},
				{
					Owner:     owner,
					OwnerType: ownerType,
					Message:   core.ChatMessage{Role: core.RoleAssistant, Content: replyText},
				},
			})
		}
	} else {
		replyText = "智能体引擎未就绪"
	}

	if receiptText != "" {
		replyText = replyText + "\n\n" + receiptText
	}

	sendDirectReply(event, conn, replyText)
}

func sendDirectReply(event Event, conn *wsConn, text string) {
	if conn == nil || strings.TrimSpace(text) == "" {
		return
	}
	targetID := event.UserID
	if event.MessageType == "group" {
		targetID = event.GroupID
	}
	action := Action{
		Type:           "action",
		Action:         "send_message",
		SessionID:      sessionKey(event),
		TargetID:       targetID,
		MessageType:    event.MessageType,
		GroupID:        event.GroupID,
		UserID:         event.UserID,
		Content:        text,
		IsIntermediate: false,
		Echo:           fmt.Sprintf("reply_%s", event.MessageID),
	}
	if err := conn.WriteJSON(action); err != nil {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot 发送回复失败: %v", err))
	}
}
