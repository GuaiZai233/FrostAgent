package astrbot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/adapter/parity"
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
	"FrostAgent/internal/tools"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

var nextConnGeneration uint64

type wsConn struct {
	*runtimescope.Scope
	conn         *websocket.Conn
	generation   string
	inFlight     sync.WaitGroup
	closed       atomic.Bool
	ctx          context.Context
	cancel       context.CancelFunc
	writeMu      sync.Mutex
	mock         bool
	mockSessions sync.Map
}

func newWSConn(conn *websocket.Conn, scopes ...*runtimescope.Scope) *wsConn {
	gen := fmt.Sprintf("astrbot-conn-%d", atomic.AddUint64(&nextConnGeneration, 1))
	parentCtx := context.Background()
	var scope *runtimescope.Scope
	if len(scopes) > 0 && scopes[0] != nil {
		scope = scopes[0]
		if scope.Context() != nil {
			parentCtx = scope.Context()
		}
	}
	ctx, cancel := context.WithCancel(parentCtx)
	return &wsConn{
		Scope:      scope,
		conn:       conn,
		generation: gen,
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (c *wsConn) isClosed() bool {
	if c == nil {
		return true
	}
	return c.closed.Load()
}

func (c *wsConn) Context() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *wsConn) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil || c.isClosed() {
		return errors.New("connection closed")
	}
	return c.conn.WriteMessage(messageType, data)
}

func (c *wsConn) WriteJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil || c.isClosed() {
		return errors.New("connection closed")
	}

	// Action.MarshalJSON retains process-env behavior for legacy direct callers.
	// Instance-bound WebSocket delivery must normalize with this connection's
	// runtime scope, then marshal an alias so MarshalJSON cannot re-read globals.
	type actionAlias Action
	switch action := v.(type) {
	case Action:
		normalized := action.withConfiguredGroupMentionScope(c.Scope).withConfiguredGroupReplyScope(c.Scope)
		v = actionAlias(normalized)
	case *Action:
		if action != nil {
			normalized := action.withConfiguredGroupMentionScope(c.Scope).withConfiguredGroupReplyScope(c.Scope)
			v = actionAlias(normalized)
		}
	}
	return c.conn.WriteJSON(v)
}

func (c *wsConn) Close() error {
	if c == nil {
		return nil
	}
	if c.closed.CompareAndSwap(false, true) {
		if c.cancel != nil {
			c.cancel()
		}
	}
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *wsConn) sessionKey(event Event) string {
	baseKey := sessionKey(event)
	if c != nil && c.mock {
		key := fmt.Sprintf("mock:%s:%s", c.generation, baseKey)
		c.mockSessions.Store(key, struct{}{})
		return key
	}
	return baseKey
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

func captureGroupCompactText(event Event, text string, engine *llm.Engine) {
	if engine == nil || event.GroupID == "" || strings.TrimSpace(text) == "" {
		return
	}
	session := engine.SessionManager.GetOrCreate(sessionKey(event))
	var maxBufferSize int
	if engine.GroupCompactor != nil {
		maxBufferSize = engine.GroupCompactor.MaxBufferSize()
	}
	session.AppendGroupCompactMessage(
		llm.GroupCompactMessage{
			Role:      "user",
			Sender:    senderDisplayName(event),
			SenderID:  event.UserID,
			Content:   strings.TrimSpace(text),
			MessageID: event.MessageID,
			Time:      time.Now().Format("15:04:05"),
		},
		maxBufferSize,
	)
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	if engine.GroupCompactor != nil {
		owner, _ := memory.OwnerForPlatformGroup(platform, event.GroupID)
		engine.GroupCompactor.TriggerWithScope(session, owner, astrBotRouteScope(event))
	}
}

func captureGroupCompactMessage(event Event, engine *llm.Engine) {
	if engine == nil || event.GroupID == "" {
		return
	}
	visibleText := astrBotVisibleText(event)
	captureGroupCompactText(event, visibleText, engine)
}

func stageGroupCompactText(event Event, text string, engine *llm.Engine) {
	if engine == nil || event.GroupID == "" || strings.TrimSpace(text) == "" {
		return
	}
	session := engine.SessionManager.GetOrCreate(sessionKey(event))
	var maxBufferSize int
	if engine.GroupCompactor != nil {
		maxBufferSize = engine.GroupCompactor.MaxBufferSize()
	}
	session.StageGroupCompactMessage(
		llm.GroupCompactMessage{
			Role:      "user",
			Sender:    senderDisplayName(event),
			SenderID:  event.UserID,
			Content:   strings.TrimSpace(text),
			MessageID: event.MessageID,
			Time:      time.Now().Format("15:04:05"),
		},
		maxBufferSize,
		event.MessageID,
	)
}

func stageGroupCompactMessage(event Event, engine *llm.Engine) {
	if engine == nil || event.GroupID == "" {
		return
	}
	visibleText := astrBotVisibleText(event)
	stageGroupCompactText(event, visibleText, engine)
}

func stageAssistantGroupMessage(session *llm.SessionContext, engine *llm.Engine, replyText, messageID string) {
	replyText = strings.TrimSpace(replyText)
	if session == nil || engine == nil || replyText == "" {
		return
	}

	botName := engine.Getenv("BOT_NAME")
	if botName == "" {
		botName = "霜降"
	}
	var maxBufferSize int
	if engine.GroupCompactor != nil {
		maxBufferSize = engine.GroupCompactor.MaxBufferSize()
	}
	session.StageGroupCompactMessage(
		llm.GroupCompactMessage{
			Role:      "assistant",
			Sender:    botName,
			Content:   replyText,
			MessageID: messageID,
			Time:      time.Now().Format("15:04:05"),
		},
		maxBufferSize,
		messageID,
	)
}

func astrBotVisibleText(event Event) string {
	parts := make([]string, 0, 3)
	if text := strings.TrimSpace(event.Content); text != "" {
		parts = append(parts, text)
	}
	for _, att := range event.Attachments {
		if att.Type == core.AttachmentTypeImage && (att.MessageID == "" || att.MessageID == event.MessageID) {
			parts = append(parts, "[图片]")
		}
	}
	if event.Metadata != nil {
		if replyMessageID, ok := event.Metadata["reply_message_id"].(string); ok && strings.TrimSpace(replyMessageID) != "" {
			parts = append(parts, fmt.Sprintf("[回复:%s]", strings.TrimSpace(replyMessageID)))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func astrBotLogText(event Event) (string, []logs.InlineImage) {
	parts := make([]string, 0, 3)
	if text := strings.TrimSpace(event.Content); text != "" {
		parts = append(parts, text)
	}
	images := make([]logs.InlineImage, 0, len(event.Attachments))
	for _, att := range event.Attachments {
		if att.Type != core.AttachmentTypeImage || (att.MessageID != "" && att.MessageID != event.MessageID) {
			continue
		}
		if len(att.Content) > 0 {
			images = append(images, logs.InlineImage{
				ContentType: att.MimeType,
				Data:        att.Content,
			})
		} else {
			parts = append(parts, "[图片]")
		}
	}
	if event.Metadata != nil {
		if replyMessageID, ok := event.Metadata["reply_message_id"].(string); ok && strings.TrimSpace(replyMessageID) != "" {
			parts = append(parts, fmt.Sprintf("[回复:%s]", strings.TrimSpace(replyMessageID)))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " ")), images
}

func formatGroupSpeakerMessage(event Event, text string) string {
	return fmt.Sprintf("[user] %s (%s): %s", senderDisplayName(event), event.UserID, strings.TrimSpace(text))
}

func formatGroupAssistantMessage(botName, text string) string {
	if botName == "" {
		botName = "霜降"
	}
	return fmt.Sprintf("[assistant] %s: %s", botName, strings.TrimSpace(text))
}

func extractBotReplyText(replyText string) string {
	var toolOutput struct {
		Messages []tools.Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(replyText), &toolOutput); err == nil && len(toolOutput.Messages) > 0 {
		var texts []string
		for _, m := range toolOutput.Messages {
			if m.Type == "plain" && strings.TrimSpace(m.Text) != "" {
				texts = append(texts, strings.TrimSpace(m.Text))
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, " ")
		}
		return ""
	}
	return strings.TrimSpace(replyText)
}

func appendAssistantGroupMessage(session *llm.SessionContext, engine *llm.Engine, owner, replyText string, routeScope modelrouter.Scope) {
	replyText = strings.TrimSpace(replyText)
	if session == nil || engine == nil || replyText == "" {
		return
	}

	botName := engine.Getenv("BOT_NAME")
	if botName == "" {
		botName = "霜降"
	}
	var maxBufferSize int
	if engine.GroupCompactor != nil {
		maxBufferSize = engine.GroupCompactor.MaxBufferSize()
	}
	session.AppendGroupCompactMessage(
		llm.GroupCompactMessage{
			Role:    "assistant",
			Sender:  botName,
			Content: replyText,
			Time:    time.Now().Format("15:04:05"),
		},
		maxBufferSize,
	)
	if engine.GroupCompactor != nil {
		engine.GroupCompactor.TriggerWithScope(session, owner, routeScope)
	}
}

func composeReplyWithReceipt(replyText, receiptText string) string {
	if receiptText == "" {
		return replyText
	}
	if strings.TrimSpace(replyText) == "" {
		return receiptText
	}
	return replyText + "\n\n" + receiptText
}

func isBotNameMentioned(text string, scopes ...*runtimescope.Scope) bool {
	if text == "" {
		return false
	}
	names := configuredBotNames(scopes...)
	for _, name := range names {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}

func configuredBotNames(scopes ...*runtimescope.Scope) []string {
	scope := runtimescope.First(scopes)
	name, nameSet := scope.LookupEnv("BOT_NAME")
	if !nameSet {
		name = "霜降狐"
	}
	aliases, aliasesSet := scope.LookupEnv("BOT_ALIASES")
	if !aliasesSet {
		aliases = "霜降,FrostAgent"
	}

	values := append([]string{name}, strings.Split(aliases, ",")...)
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func shouldReply(event Event, scopes ...*runtimescope.Scope) bool {
	if event.Metadata != nil {
		if val, ok := event.Metadata["_frostagent_should_reply"]; ok {
			if b, ok := val.(bool); ok {
				return b
			}
		}
	}
	scope := runtimescope.First(scopes)
	if event.MessageType == "private" {
		return true
	}
	if event.MessageType == "group" {
		if scope.Getenv("GROUP_REPLY_ON_MENTION") == "false" {
			return false
		}
		if event.IsWake || event.IsAt {
			return true
		}
		if isBotNameMentioned(event.Content, scope) {
			return true
		}
		return slices.ContainsFunc(event.Messages, func(text string) bool { return isBotNameMentioned(text, scope) })
	}
	return true
}

func isMentionOnlyInteraction(event Event) bool {
	hasReply := false
	hasOtherMention := false
	hasOtherContent := false
	hasMediaContent := false
	if event.Metadata != nil {
		if replyMessageID, ok := event.Metadata["reply_message_id"].(string); ok && strings.TrimSpace(replyMessageID) != "" {
			hasReply = true
		}
		if v, ok := event.Metadata["has_other_mention"]; ok {
			switch val := v.(type) {
			case bool:
				hasOtherMention = val
			case string:
				hasOtherMention = strings.EqualFold(strings.TrimSpace(val), "true")
			}
		}
		if v, ok := event.Metadata["has_other_content"]; ok {
			switch val := v.(type) {
			case bool:
				hasOtherContent = val
			case string:
				hasOtherContent = strings.EqualFold(strings.TrimSpace(val), "true")
			}
		}
		if v, ok := event.Metadata["has_media_content"]; ok {
			switch val := v.(type) {
			case bool:
				hasMediaContent = val
			case string:
				hasMediaContent = strings.EqualFold(strings.TrimSpace(val), "true")
			}
		} else if v, ok := event.Metadata["has_images"]; ok {
			switch val := v.(type) {
			case bool:
				hasMediaContent = val
			case string:
				hasMediaContent = strings.EqualFold(strings.TrimSpace(val), "true")
			}
		}
	}
	hasImages := len(event.Attachments) > 0 || hasMediaContent
	return parity.IsMentionOnlyAstrBot(
		event.MessageType == "group",
		event.IsAt,
		event.Content,
		hasImages,
		hasReply,
		hasOtherMention,
		hasOtherContent,
	)
}

func processEvent(conn *wsConn, event Event, engine *llm.Engine, turn *llm.SessionTurn, routeSnapshot *modelrouter.Snapshot, warningNotice ...string) {
	if conn != nil && conn.mock && conn.isClosed() {
		return
	}
	var startEpoch uint64
	if turn != nil {
		startEpoch = turn.Epoch()
		turn.Wait()
		defer turn.Done()
		if engine != nil && engine.SessionManager != nil {
			if sessCore, ok := engine.SessionManager.Get(conn.sessionKey(event)); ok {
				if sess, isSess := sessCore.(*llm.SessionContext); isSess {
					if !turn.IsValid(sess) {
						if conn != nil {
							platform := event.Platform
							if platform == "" {
								platform = "astrbot"
							}
							_ = conn.WriteJSON(Action{
								Type:      "action",
								Action:    "noop",
								Platform:  platform,
								SessionID: conn.sessionKey(event),
								Echo:      "reply_" + event.MessageID,
							})
						}
						return
					}
				}
			}
		}
	} else if engine != nil && engine.SessionManager != nil {
		if sessCore, ok := engine.SessionManager.Get(conn.sessionKey(event)); ok {
			if sess, isSess := sessCore.(*llm.SessionContext); isSess {
				startEpoch = sess.Epoch()
			}
		}
	}
	if conn != nil && conn.mock && conn.isClosed() {
		return
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

	if !shouldReply(event, engine.Scope) {
		engine.Log().Debug(
			logs.WEBSOCKET,
			fmt.Sprintf(
				"AstrBot 忽略未唤醒群聊消息 (ID:%s Group:%s User:%s): %s",
				event.MessageID,
				event.GroupID,
				event.UserID,
				event.Content,
			),
		)
		_ = conn.WriteJSON(Action{
			Type:      "action",
			Action:    "noop",
			Platform:  platform,
			SessionID: event.SessionID,
			Echo:      "reply_" + event.MessageID,
		})
		return
	}

	logText, logImages := astrBotLogText(event)
	engine.Log().InfoWithInlineImages(
		logs.WEBSOCKET,
		fmt.Sprintf(
			"AstrBot 收到 [%s] %s 消息 (ID:%s User:%s/%s Group:%s): %s",
			platform,
			event.MessageType,
			event.MessageID,
			event.UserID,
			senderDisplayName(event),
			event.GroupID,
			logText,
		),
		logImages,
	)

	replyWithSnapshot(event, engine, conn, routeSnapshot, startEpoch, warningNotice...)
}

func reply(event Event, engine *llm.Engine, conn *wsConn, warningNotice ...string) {
	var snapshot *modelrouter.Snapshot
	if engine != nil && engine.ModelRouter != nil {
		snapshot = engine.ModelRouter.Snapshot()
	}
	var startEpoch uint64
	if engine != nil && engine.SessionManager != nil {
		if sessCore, ok := engine.SessionManager.Get(conn.sessionKey(event)); ok {
			if sess, isSess := sessCore.(*llm.SessionContext); isSess {
				startEpoch = sess.Epoch()
			}
		}
	}
	replyWithSnapshot(event, engine, conn, snapshot, startEpoch, warningNotice...)
}

func replyWithSnapshot(event Event, engine *llm.Engine, conn *wsConn, routeSnapshot *modelrouter.Snapshot, startEpoch uint64, warningNotice ...string) {
	if conn != nil && conn.mock && conn.isClosed() {
		return
	}
	var notice string
	if len(warningNotice) > 0 {
		notice = warningNotice[0]
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	securityPlatform := platform
	if conn.mock {
		securityPlatform = "mock"
	}
	evalCtx := engine.Security.EvaluateContext
	if conn.mock {
		evalCtx = engine.Security.EvaluateContextDryRun
	}
	routeScope := astrBotRouteScope(event)
	routeCtx := runtimescope.WithContext(engine.Context(), engine.Scope)
	if engine != nil && engine.ModelRouter != nil {
		routeCtx = engine.ModelRouter.WithSnapshot(routeCtx, routeSnapshot)
	}
	userText := astrBotVisibleText(event)

	if engine != nil && engine.SessionManager != nil {
		if sessCore, ok := engine.SessionManager.Get(conn.sessionKey(event)); ok {
			if sess, isSess := sessCore.(*llm.SessionContext); isSess && sess.Epoch() != startEpoch {
				return
			}
		}
	}

	// 处理图片等多模态内容描述
	var imageSegments []content.MessageSegment
	for _, att := range event.Attachments {
		if att.Type == core.AttachmentTypeImage && (len(att.Content) > 0 || att.URL != "") {
			data := make(map[string]any)
			if len(att.Content) > 0 {
				data["base64"] = base64.StdEncoding.EncodeToString(att.Content)
			} else {
				data["url"] = att.URL
			}
			imageSegments = append(imageSegments, content.MessageSegment{
				Type: "image",
				Data: data,
			})
		}
	}

	if len(imageSegments) > 0 && engine != nil {
		visionEnabled := engine.VisionProvider != nil
		if visionEnabled && routeSnapshot != nil {
			visionEnabled = !routeSnapshot.IsDisabled(modelrouter.WorkloadVision, routeScope)
		}
		if !visionEnabled {
			imageSegments = nil
		} else {
			// 计费检查 (视觉处理前检查)
			billingPlatform := parity.CanonicalBillingPlatform(event.Platform)
			if engine.BillingClient != nil && engine.BillingConfig.Enabled && !conn.mock {
				bCtx, bCancel := context.WithTimeout(runtimescope.WithContext(engine.Context(), engine.Scope), engine.BillingConfig.Timeout)
				bal, err := engine.BillingClient.Balance(bCtx, billingPlatform, event.UserID)
				bCancel()
				if err != nil {
					if errors.Is(err, billing.ErrInsufficientFunds) {
						engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 用户 [%s] 雪花余额不足，拒绝视觉处理", event.UserID))
						sendDirectReply(event, conn, billing.FormatInsufficientFundsMessage(0))
						return
					}
					engine.Log().Error(logs.SYSTEM, fmt.Sprintf("Alcyone 计费服务不可用 (fail-closed, vision): %v", err))
					sendDirectReply(event, conn, billing.FormatBillingUnavailableMessage())
					return
				}
				if bal != nil && bal.Exists && bal.BalanceMinor <= 0 {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 用户 [%s] 余额为 0，拒绝视觉处理", event.UserID))
					sendDirectReply(event, conn, billing.FormatInsufficientFundsMessage(0))
					return
				}
			}

			imageDesc := content.ProcessImage(routeCtx, imageSegments, engine.VisionProvider, core.RouteContext{Platform: routeScope.Platform, GroupID: routeScope.GroupID})
			if imageDesc != "" {
				if engine != nil && engine.Security != nil {
					principal, pErr := security.NewPrincipal(securityPlatform, event.UserID)
					if pErr == nil {
						decision := evalCtx(principal, security.SourceVisionResult, imageDesc, security.AuditEvent{
							Instance: engine.InstanceID,
							Session: conn.sessionKey(event),
						})
						if decision.IsFailure {
							engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 视觉处理结果安全审查服务异常 (eval_id=%s)，保守隔离剔除", decision.EvaluationID))
							imageDesc = ""
						} else if security.Blocks(decision.Action) || decision.Action == security.WatchdogFilter {
							engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 视觉处理结果包含敏感内容，已被安全机制降级脱敏: %s", decision.Reason))
							imageDesc = decision.SanitizedContent
						} else if decision.Action == security.WatchdogWarn && notice == "" {
							notice = decision.WarningNotice
						}
					}
				}
			}
			if imageDesc != "" {
				userText = strings.TrimSpace(userText + " 【图片内容】：" + imageDesc)
			}
		}
	}

	if engine != nil && engine.SessionManager != nil {
		if sessCore, ok := engine.SessionManager.Get(conn.sessionKey(event)); ok {
			if sess, isSess := sessCore.(*llm.SessionContext); isSess && sess.Epoch() != startEpoch {
				return
			}
		}
	}

	var session *llm.SessionContext
	var groupSnapshot llm.GroupContextSnapshot
	if engine != nil && engine.SessionManager != nil {
		if conn != nil && conn.mock && conn.isClosed() {
			return
		}
		session = engine.SessionManager.GetOrCreate(conn.sessionKey(event))
		if session != nil && session.Epoch() != startEpoch {
			return
		}
		if event.MessageType == "group" {
			limit := engine.GroupRawLimit()
			maxChars := engine.GroupRawMaxChars()
			groupSnapshot = session.SnapshotGroupContext(limit, maxChars, event.MessageID)
		}
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

	senderName := senderDisplayName(event)
	groupName := event.GroupName
	if engine != nil && engine.Security != nil {
		principal, pErr := security.NewPrincipal(securityPlatform, event.UserID)
		if pErr == nil {
			if senderName != "" {
				decision := evalCtx(principal, security.SourcePlatformMeta, senderName, security.AuditEvent{
					Instance: engine.InstanceID,
					Session: conn.sessionKey(event),
				})
				if decision.IsFailure {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 发送者名称安全审查服务异常 (eval_id=%s)，保守隔离剔除", decision.EvaluationID))
					senderName = ""
				} else if security.Blocks(decision.Action) || decision.Action == security.WatchdogFilter {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 发送者名称 [%s] 包含敏感内容，已被安全机制降级脱敏", senderName))
					senderName = decision.SanitizedContent
				} else if decision.Action == security.WatchdogWarn && notice == "" {
					notice = decision.WarningNotice
				}
			}
			if groupName != "" {
				decision := evalCtx(principal, security.SourcePlatformMeta, groupName, security.AuditEvent{
					Instance: engine.InstanceID,
					Session: conn.sessionKey(event),
				})
				if decision.IsFailure {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 群名称安全审查服务异常 (eval_id=%s)，保守隔离剔除", decision.EvaluationID))
					groupName = ""
				} else if security.Blocks(decision.Action) || decision.Action == security.WatchdogFilter {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 群名称 [%s] 包含敏感内容，已被安全机制降级脱敏", groupName))
					groupName = decision.SanitizedContent
				} else if decision.Action == security.WatchdogWarn && notice == "" {
					notice = decision.WarningNotice
				}
			}
		}
	}

	contextData := map[string]any{
		"message_id": event.MessageID,
		"sender_id":  event.UserID,
		"platform":   platform,
	}
	if senderName != "" {
		contextData["sender_name"] = senderName
	}
	if event.Metadata != nil {
		if replyMessageID, ok := event.Metadata["reply_message_id"].(string); ok && strings.TrimSpace(replyMessageID) != "" {
			contextData["reply_message_id"] = strings.TrimSpace(replyMessageID)
		}
	}
	if event.MessageType == "group" {
		contextData["group_id"] = event.GroupID
		if groupName != "" {
			contextData["group_name"] = groupName
		}
		contextData["is_wake"] = event.IsWake
		contextData["is_at"] = event.IsAt
		mentionOnly := isMentionOnlyInteraction(event)
		contextData["mention_only"] = mentionOnly
		if mentionOnly {
			contextData["interaction_guidance"] = parity.MentionOnlyGuidance
		}
	}
	contextBytes, _ := json.Marshal(contextData)

	durablePrompt := fmt.Sprintf("User Message: %s\n\n<system_context>\n%s\n</system_context>", userText, string(contextBytes))

	requestPrompt := fmt.Sprintf("User Message: %s", userText)
	if session != nil {
		if deliveryFailure := session.TakeDeliveryFailure(); deliveryFailure != nil {
			deliveryTag := deliveryFailure.FormatDeliveryContext()
			if deliveryTag != "" {
				requestPrompt += fmt.Sprintf("\n\n%s", deliveryTag)
			}
		}
	}
	var vettedSummary string
	if groupSnapshot.RunningSummary != "" {
		vettedSummary = groupSnapshot.RunningSummary
		if engine != nil && engine.Security != nil {
			principal, pErr := security.NewPrincipal(securityPlatform, event.UserID)
			if pErr == nil {
				decision := evalCtx(principal, security.SourceGroupContext, groupSnapshot.RunningSummary, security.AuditEvent{
					Instance: engine.InstanceID,
					Session: conn.sessionKey(event),
				})
				if decision.IsFailure {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 群 [%s] 摘要安全审查服务异常 (eval_id=%s)，保守隔离剔除", event.GroupID, decision.EvaluationID))
					vettedSummary = ""
				} else if security.Blocks(decision.Action) || decision.Action == security.WatchdogFilter {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 群 [%s] 摘要包含敏感内容，已被安全机制降级脱敏", event.GroupID))
					vettedSummary = decision.SanitizedContent
				} else if decision.Action == security.WatchdogWarn && notice == "" {
					notice = decision.WarningNotice
				}
			}
		}
	}
	if vettedSummary != "" {
		requestPrompt += fmt.Sprintf(
			"\n\n<group_running_summary>\n%s\n</group_running_summary>",
			vettedSummary,
		)
	}
	if recentContext := llm.FormatRecentGroupMessagesContext(groupSnapshot.RecentStructuredMessages); recentContext != "" {
		vettedRecent := recentContext
		if engine != nil && engine.Security != nil {
			principal, pErr := security.NewPrincipal(securityPlatform, event.UserID)
			if pErr == nil {
				decision := evalCtx(principal, security.SourceGroupContext, recentContext, security.AuditEvent{
					Instance: engine.InstanceID,
					Session: conn.sessionKey(event),
				})
				if decision.IsFailure {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 群 [%s] 最近历史消息安全审查服务异常 (eval_id=%s)，保守隔离剔除", event.GroupID, decision.EvaluationID))
					vettedRecent = ""
				} else if security.Blocks(decision.Action) || decision.Action == security.WatchdogFilter {
					engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("AstrBot 群 [%s] 最近历史消息包含敏感内容，已被安全机制降级脱敏", event.GroupID))
					vettedRecent = decision.SanitizedContent
				} else if decision.Action == security.WatchdogWarn && notice == "" {
					notice = decision.WarningNotice
				}
			}
		}
		if vettedRecent != "" {
			requestPrompt += "\n\n" + vettedRecent
		}
	}
	requestPrompt += fmt.Sprintf("\n\n<system_context>\n%s\n</system_context>", string(contextBytes))

	var (
		replyText            string
		receiptText          string
		deliveredToolReplies []string
		runResult            llm.AgentRunResult
	)

	if engine != nil && session != nil {
		var billingState *llm.BillingRunState
		if engine.BillingClient != nil && engine.BillingConfig.Enabled && !conn.mock {
			billingPlatform := parity.CanonicalBillingPlatform(event.Platform)
			taskID := parity.BillingTaskID(billingPlatform, event.UserID, event.MessageID)
			billingState = &llm.BillingRunState{
				Platform:      billingPlatform,
				ExternalID:    event.UserID,
				DisplayName:   senderDisplayName(event),
				TaskID:        taskID,
				BillingActive: true,
			}
		}

		session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: durablePrompt})
		messages := session.Snapshot()
		if len(messages) > 0 {
			messages[len(messages)-1].Content = requestPrompt
		}

		targetID := event.UserID
		if event.MessageType == "group" {
			targetID = event.GroupID
		}

		sendHook := func(toolResultJSON string) error {
			var toolOutput struct {
				Messages []tools.Msg `json:"messages"`
			}
			if err := json.Unmarshal([]byte(toolResultJSON), &toolOutput); err != nil {
				engine.Log().Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot SendHook: 解析 send_message 结果失败: %v", err))
				return fmt.Errorf("解析 send_message 结果失败: %w", err)
			}
			actionMessages := make([]ActionMessage, 0, len(toolOutput.Messages))
			var plainTexts []string
			var attachments []core.Attachment
			for _, m := range toolOutput.Messages {
				actionMessages = append(actionMessages, actionMessageFromToolMessage(m))
				switch m.Type {
				case "plain":
					plainTexts = append(plainTexts, m.Text)
				case "image":
					url := m.URL
					if url == "" {
						url = m.Path
					}
					att := core.Attachment{
						Type: core.AttachmentTypeImage,
						URL:  url,
					}
					if m.IsSticker {
						att.SubType = 1
					}
					attachments = append(attachments, att)
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
				}
			}
			action := Action{
				Type:           "action",
				Action:         "send_message",
				Platform:       platform,
				SessionID:      conn.sessionKey(event),
				TargetID:       targetID,
				MessageType:    event.MessageType,
				GroupID:        event.GroupID,
				UserID:         event.UserID,
				Content:        strings.Join(plainTexts, ""),
				Messages:       actionMessages,
				Attachments:    attachments,
				IsIntermediate: true,
				Echo:           fmt.Sprintf("hook_%s", event.MessageID),
				ReplyMessageID: event.MessageID,
			}
			if session != nil && session.Epoch() != startEpoch {
				return errors.New("会话已重置，取消发送中间消息")
			}
			if err := conn.WriteJSON(action); err != nil {
				engine.Log().Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot SendHook: 发送消息失败: %v", err))
				return err
			}
			if session != nil && session.Epoch() != startEpoch {
				return errors.New("会话已重置，取消发送中间消息")
			}
			if deliveredReply := extractBotReplyText(toolResultJSON); strings.TrimSpace(deliveredReply) != "" {
				deliveredToolReplies = append(deliveredToolReplies, deliveredReply)
				if event.MessageType == "group" && !conn.mock {
					stageAssistantGroupMessage(session, engine, deliveredReply, event.MessageID)
				}
			}
			return nil
		}

		var runCtx context.Context
		if conn != nil && conn.mock {
			runCtx = conn.Context()
		}
		runResult = engine.RunMessagesWithContext(messages, llm.RunContext{
			Context:        runCtx,
			Epoch:          startEpoch,
			SessionID:      conn.sessionKey(event),
			Owner:          owner,
			OwnerType:      ownerType,
			ActorUserID:    event.UserID,
			ActorPlatform:  securityPlatform,
			InstanceID:     engine.InstanceID,
			SendHook:       sendHook,
			SecurityNotice: notice,
			LoadObservedSticker: func(ctx context.Context, messageID string, stickerIndex int) ([]byte, error) {
				return loadObservedStickerFromEvent(ctx, event, messageID, stickerIndex)
			},
			Billing:       billingState,
			RouteScope:    routeScope,
			RouteSnapshot: routeSnapshot,
			Mock:          conn.mock,
		})
		replyText = runResult.Content
		if session != nil && session.Epoch() != startEpoch {
			return
		}

		if runResult.Banned {
			if session != nil {
				session.DropLastMessage()
				if event.MessageType == "group" {
					session.DropGroupCompactMessage(event.MessageID, event.UserID)
					if engine != nil && engine.GroupCompactor != nil {
						engine.GroupCompactor.RollbackPersistence(owner, session.GroupRunningSummary())
					}
				}
			}
			_ = sendDirectReply(event, conn, runResult.Content)
			return
		}

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
			engine.TrimSession(session)
			if event.MessageType == "group" && !runResult.Banned {
				if session != nil {
					session.PromoteGroupCompactMessage(event.MessageID)
					if engine != nil && engine.GroupCompactor != nil && !conn.mock {
						engine.GroupCompactor.TriggerWithScope(session, owner, routeScope)
					}
				}
			}
			engine.Log().Info(logs.SYSTEM, fmt.Sprintf("AstrBot: 本轮保持沉默: session=%s", conn.sessionKey(event)))
			return
		}

	} else {
		replyText = "智能体引擎未就绪"
	}

	historyReplyText := replyText
	if strings.TrimSpace(historyReplyText) == "" && len(deliveredToolReplies) > 0 {
		historyReplyText = strings.Join(deliveredToolReplies, "\n")
	}
	sentReplyText := composeReplyWithReceipt(replyText, receiptText)

	// AstrBot WebSocket 协议目前为单向动作通知，不具备 OneBot 的同步请求-响应平台 ACK。
	// 此处 sendDirectReply 校验传输层 Socket 写入成功 (conn.WriteJSON transport-write confirmation)。
	if session != nil && session.Epoch() != startEpoch {
		return
	}
	var sendErr error
	if strings.TrimSpace(sentReplyText) == "" {
		sendErr = sendTerminalNoop(event, conn)
	} else {
		sendErr = sendDirectReply(event, conn, sentReplyText)
	}
	if session != nil && session.Epoch() != startEpoch {
		return
	}
	if sendErr != nil {
		if session != nil {
			if event.MessageType == "group" {
				session.DropGroupCompactMessage(event.MessageID, event.UserID)
			}
			session.SetDeliveryFailure(llm.DeliveryFailure{
				Platform: platform,
				Action:   "send_message",
				Wording:  sendErr.Error(),
			})
			if engine != nil {
				engine.TrimSession(session)
			}
		}
		return
	}

	if engine == nil || session == nil {
		return
	}
	if session.Epoch() != startEpoch {
		return
	}

	if event.MessageType == "group" && !runResult.Banned {
		if session != nil {
			session.PromoteGroupCompactMessage(event.MessageID)
		}
	}
	if strings.TrimSpace(historyReplyText) != "" {
		session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: historyReplyText})
	}
	engine.TrimSession(session)

	if runResult.MemoryWritten {
		engine.Log().InfoWithConsoleSummary(logs.SYSTEM, "AstrBot: 本轮已通过 memory.write 处理记忆，跳过自动提取累计", "AstrBot: 本轮已通过 memory.write 处理记忆，跳过自动提取累计")
	} else if !conn.mock && strings.TrimSpace(userText) != "" && strings.TrimSpace(historyReplyText) != "" {
		pendingUserText := userText
		if event.MessageType == "group" {
			pendingUserText = formatGroupSpeakerMessage(event, userText)
		}
		engine.EnqueueExtractionTurn(session, []memory.PendingExtractionItem{
			{
				Owner:     owner,
				OwnerType: ownerType,
				Route:     core.RouteContext{Platform: routeScope.Platform, GroupID: routeScope.GroupID},
				Message:   core.ChatMessage{Role: core.RoleUser, Content: pendingUserText},
			},
			{
				Owner:     owner,
				OwnerType: ownerType,
				Route:     core.RouteContext{Platform: routeScope.Platform, GroupID: routeScope.GroupID},
				Message:   core.ChatMessage{Role: core.RoleAssistant, Content: historyReplyText},
			},
		})
	}

	if event.MessageType == "group" && !conn.mock {
		botReply := extractBotReplyText(replyText)
		if strings.TrimSpace(botReply) != "" {
			appendAssistantGroupMessage(session, engine, owner, botReply, routeScope)
		} else if engine != nil && engine.GroupCompactor != nil {
			engine.GroupCompactor.TriggerWithScope(session, owner, routeScope)
		}
	}
}

func actionMessageFromToolMessage(m tools.Msg) ActionMessage {
	path := m.Path
	if m.IsSticker {
		// Sticker files belong to FrostAgent's private storage. AstrBot receives
		// the HTTP endpoint instead and resolves it into OneBot-compatible data.
		path = ""
	}
	return ActionMessage{
		Type:          m.Type,
		Text:          m.Text,
		MentionUserID: m.MentionUserID,
		MessageID:     m.MessageID,
		Path:          path,
		URL:           m.URL,
		IsSticker:     m.IsSticker,
	}
}

func astrBotRouteScope(event Event) modelrouter.Scope {
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	scope := modelrouter.Scope{Platform: platform}
	if event.MessageType == "group" {
		scope.GroupID = event.GroupID
	}
	return scope
}

func sendTerminalNoop(event Event, conn *wsConn) error {
	if conn == nil {
		return errors.New("connection is nil")
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	return conn.WriteJSON(Action{
		Type:      "action",
		Action:    "noop",
		Platform:  platform,
		SessionID: conn.sessionKey(event),
		Echo:      "reply_" + event.MessageID,
	})
}

// sendDirectReply sends a direct message to the AstrBot WebSocket connection.
// Note: AstrBot WS protocol does not have a request-response platform delivery ACK.
// This confirms WebSocket transport write success (best-effort delivery).
func sendDirectReply(event Event, conn *wsConn, text string) error {
	if conn == nil {
		return errors.New("connection is nil")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("message content is empty")
	}
	targetID := event.UserID
	if event.MessageType == "group" {
		targetID = event.GroupID
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	action := Action{
		Type:           "action",
		Action:         "send_message",
		Platform:       platform,
		SessionID:      conn.sessionKey(event),
		TargetID:       targetID,
		MessageType:    event.MessageType,
		GroupID:        event.GroupID,
		UserID:         event.UserID,
		Content:        text,
		IsIntermediate: false,
		Echo:           fmt.Sprintf("reply_%s", event.MessageID),
		ReplyMessageID: event.MessageID,
	}
	if err := conn.WriteJSON(action); err != nil {
		conn.Log().Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot 发送回复失败: %v", err))
		return err
	}
	return nil
}
