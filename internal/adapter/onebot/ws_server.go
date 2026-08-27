package onebot

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
	"strconv"
	"strings"
	"sync"
	"time"

	"FrostAgent/internal/model"
	"FrostAgent/internal/modelrouter"
	"github.com/gorilla/websocket"
)

// upgrader restricts browser WebSocket origins. Configure extra trusted origins
// with WS_ALLOWED_ORIGINS as a comma-separated list, for example:
// https://bot.example.com,https://admin.example.com
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
		// Non-browser OneBot implementations often omit Origin; keep them working.
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("拒绝 WebSocket 连接：非法 Origin %q", origin))
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

	logs.Error(logs.WEBSOCKET, fmt.Sprintf("拒绝 WebSocket 连接：Origin %q 不在允许列表", origin))
	return false
}

// wsConnection is a thread-safe wrapper around a websocket.Conn
type wsConnection struct {
	conn               *websocket.Conn
	writeMu            sync.Mutex
	messageMu          sync.Mutex
	pendingMessage     map[string]chan oneBotAPIResponse
	nextMessageEcho    uint64
	groupMu            sync.Mutex
	groupCache         map[int64]cachedGroupInfo
	pendingGroupByEcho map[string]pendingGroupInfo
	pendingGroupByID   map[int64]string
	nextGroupEcho      uint64
}

func newWSConnection(conn *websocket.Conn) *wsConnection {
	return &wsConnection{
		conn:               conn,
		pendingMessage:     make(map[string]chan oneBotAPIResponse),
		groupCache:         make(map[int64]cachedGroupInfo),
		pendingGroupByEcho: make(map[string]pendingGroupInfo),
		pendingGroupByID:   make(map[int64]string),
	}
}

func (c *wsConnection) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("websocket connection is nil")
	}
	return c.conn.WriteMessage(messageType, data)
}

func (c *wsConnection) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Deprecated: HandleWS 是遗留的 WebSocket 入口。请改用 NewAdapter(engine).Handler()。
func HandleWS(engine *llm.Engine) http.HandlerFunc {
	adapter := NewAdapter(engine)
	if engine != nil && engine.Dispatcher != nil {
		engine.Dispatcher.RegisterAdapter(adapter)
	}
	return adapter.Handler()
}

// processEvent holds its reserved session turn until routing and reply finish.
func processEvent(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine, turn *llm.SessionTurn, routeSnapshot *modelrouter.Snapshot) {
	if turn != nil {
		turn.Wait()
		defer turn.Done()
	}
	if event.PostType != "message" {
		return
	}

	if event.MessageType == "group" {
		logs.Info(
			logs.WEBSOCKET,
			fmt.Sprintf(
				"收到群 [%d] 用户 [%d/%s] 的消息: %s",
				event.GroupID,
				event.UserID,
				senderDisplayName(event),
				string(event.Message),
			),
		)
		// 群聊被真实 @ 或名称/别名提及时触发对话（总开关；未设置视为启用）。
		// 两种唤醒信号等价；无信号消息仍已在读取协程中进入 running compact。
		if os.Getenv("GROUP_REPLY_ON_MENTION") == "false" {
			return
		}
		wakeSignals := DetectGroupWakeSignals(event)
		replyContext := conn.lookupReplyContext(event)
		if !wakeSignals.Any() && !replyContext.MentionsBot {
			return
		}
		responseContext := buildResponseContext(event, wakeSignals, replyContext.MentionsBot)
		reply("send_group_msg", "group_id", strconv.FormatInt(event.GroupID, 10), "echo_agent_req_001", event, engine, conn, replyContext.Prompt, responseContext, routeSnapshot)

	} else if event.MessageType == "private" {
		logs.Info(
			logs.WEBSOCKET,
			fmt.Sprintf(
				"收到用户 [%d/%s] 的私聊消息: %s",
				event.UserID,
				senderDisplayName(event),
				string(event.Message),
			),
		)
		replyContext := conn.lookupReplyContext(event)
		responseContext := buildResponseContext(event, GroupWakeSignals{}, false)
		reply("send_private_msg", "user_id", strconv.FormatInt(event.UserID, 10), "echo_private_001", event, engine, conn, replyContext.Prompt, responseContext, routeSnapshot)
	}
}

// reply records terminal silence without sending or batching memory.
func reply(action string, type1 string, id string, echo string, event model.OneBotEvent, engine *llm.Engine, conn *wsConnection, replyContext string, responseContext string, routeSnapshot *modelrouter.Snapshot) {
	routeScope := oneBotRouteScope(event)
	if routeSnapshot == nil && engine != nil && engine.ModelRouter != nil {
		routeSnapshot = engine.ModelRouter.Snapshot()
	}
	routeCtx := context.Background()
	if engine != nil && engine.ModelRouter != nil {
		routeCtx = engine.ModelRouter.WithSnapshot(routeCtx, routeSnapshot)
	}
	// 1. Extract user's visible message
	var segments []content.MessageSegment
	segments = []content.MessageSegment{}
	if err := json.Unmarshal(event.Message, &segments); err != nil {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("解析消息段失败: %v", err))
		// Don't return, just work with an empty segment list
	}

	userText := extractUserText(segments, event.Message)
	if content.IsContainImage(segments) {
		visionEnabled := engine != nil && engine.VisionProvider != nil
		if visionEnabled && routeSnapshot != nil {
			visionEnabled = !routeSnapshot.IsDisabled(modelrouter.WorkloadVision, routeScope)
		}
		if !visionEnabled {
			userText = strings.TrimSpace(strings.ReplaceAll(userText, "[图片]", ""))
		} else {
			// Fast-fail balance check before downloading images or calling vision LLM
			if engine != nil && engine.BillingClient != nil && engine.BillingConfig.Enabled {
				bCtx, bCancel := context.WithTimeout(context.Background(), engine.BillingConfig.Timeout)
				bal, err := engine.BillingClient.Balance(bCtx, "qq", strconv.FormatInt(event.UserID, 10))
				bCancel()
				if err != nil {
					if errors.Is(err, billing.ErrInsufficientFunds) {
						logs.Warn(logs.SYSTEM, fmt.Sprintf("用户 [%d] 雪花余额不足，拒绝视觉处理", event.UserID))
						sendDirectReply(action, type1, id, echo, event, conn, billing.FormatInsufficientFundsMessage(0))
						return
					}
					logs.Error(logs.SYSTEM, fmt.Sprintf("Alcyone 计费服务不可用 (fail-closed, vision): %v", err))
					sendDirectReply(action, type1, id, echo, event, conn, billing.FormatBillingUnavailableMessage())
					return
				}
				if bal != nil && bal.Exists && bal.BalanceMinor <= 0 {
					logs.Warn(logs.SYSTEM, fmt.Sprintf("用户 [%d] 雪花余额不足 (%d minor)，拒绝视觉处理", event.UserID, bal.BalanceMinor))
					sendDirectReply(action, type1, id, echo, event, conn, billing.FormatInsufficientFundsMessage(bal.BalanceMinor))
					return
				}
			}
			imageDesc := content.ProcessImage(routeCtx, segments, engine.VisionProvider, core.RouteContext{Platform: routeScope.Platform, GroupID: routeScope.GroupID})
			if imageDesc != "" {
				userText = userText + " 【图片内容】：" + imageDesc
			}
		}
	}

	// 检查单条用户消息输入上限保护
	if len([]rune(userText)) > 30000 {
		logs.Warn(logs.SYSTEM, fmt.Sprintf("用户 [%d] 消息过长 (%d 字)，拒绝处理", event.UserID, len([]rune(userText))))
		sendDirectReply(action, type1, id, echo, event, conn, "FrostAgent错误：单条消息长度过长，超出处理限制。")
		return
	}

	// 2. Build the implicit context as a JSON string, replicating the OneBotEvent structure
	contextMap := map[string]interface{}{
		"self_id":    event.SelfID,
		"post_type":  event.PostType,
		"user_id":    event.UserID,
		"message_id": event.MessageID,
	}
	if event.MetaEventType != "" {
		contextMap["meta_event_type"] = event.MetaEventType
	}
	if event.MessageType != "" {
		contextMap["message_type"] = event.MessageType
	}
	if event.GroupID != 0 {
		contextMap["group_id"] = event.GroupID
		if groupName := conn.groupName(event.GroupID); groupName != "" {
			contextMap["group_name"] = groupName
		}
	}
	if sender := senderContext(event); len(sender) > 0 {
		contextMap["sender"] = sender
	}
	contextBytes, _ := json.Marshal(contextMap)

	var session *llm.SessionContext
	var groupSnapshot llm.GroupContextSnapshot
	if engine != nil && engine.SessionManager != nil {
		session = engine.SessionManager.GetOrCreate(historyKey(event))
		if event.MessageType == "group" {
			limit := engine.GroupRawLimit()
			maxChars := engine.GroupRawMaxChars()
			var msgID string
			if event.MessageID != 0 {
				msgID = strconv.FormatInt(int64(event.MessageID), 10)
			}
			groupSnapshot = session.SnapshotGroupContext(limit, maxChars, msgID)
		}
	}

	// 3. Combine user text, the group running summary, and transport context.
	// Durable prompt is recorded in session history, while running summary, uncompacted
	// raw group messages, and replyContext are transient request context injected into
	// this turn's snapshot only.
	durablePrompt := fmt.Sprintf("User Message: %s", userText)
	if responseContext != "" {
		durablePrompt += fmt.Sprintf("\n\n<response_context>\n%s\n</response_context>", responseContext)
	}
	durablePrompt += fmt.Sprintf("\n\n<system_context>\n%s\n</system_context>", string(contextBytes))

	requestPrompt := fmt.Sprintf("User Message: %s", userText)
	if session != nil {
		if deliveryFailure := session.TakeDeliveryFailure(); deliveryFailure != nil {
			deliveryTag := deliveryFailure.FormatDeliveryContext()
			if deliveryTag != "" {
				requestPrompt += fmt.Sprintf("\n\n%s", deliveryTag)
			}
		}
	}
	if groupSnapshot.RunningSummary != "" {
		requestPrompt += fmt.Sprintf(
			"\n\n<group_running_summary>\n%s\n</group_running_summary>",
			groupSnapshot.RunningSummary,
		)
	}
	if recentContext := llm.FormatRecentGroupMessagesContext(groupSnapshot.RecentStructuredMessages); recentContext != "" {
		requestPrompt += "\n\n" + recentContext
	}
	if responseContext != "" {
		requestPrompt += fmt.Sprintf("\n\n<response_context>\n%s\n</response_context>", responseContext)
	}
	requestPrompt += fmt.Sprintf("\n\n<system_context>\n%s\n</system_context>", string(contextBytes))
	if replyContext != "" {
		requestPrompt += fmt.Sprintf("\n\n<reply_context>\n%s\n</reply_context>", replyContext)
	}

	// 4. Call the agent engine with history
	var (
		replyText   string
		receiptText string
		runResult   llm.AgentRunResult
	)

	owner, ownerType := memory.OwnerForPrivate(strconv.FormatInt(event.UserID, 10))
	if event.MessageType == "group" {
		owner, ownerType = memory.OwnerForGroup(event.GroupID)
	}

	if engine != nil {
		var billingState *llm.BillingRunState
		if engine.BillingClient != nil && engine.BillingConfig.Enabled {
			taskID := fmt.Sprintf("qq_%d_%d", event.UserID, event.MessageID)
			billingState = &llm.BillingRunState{
				Platform:      "qq",
				ExternalID:    strconv.FormatInt(event.UserID, 10),
				DisplayName:   senderDisplayName(event),
				TaskID:        taskID,
				BillingActive: true,
			}
		}

		// 将用户的 durable prompt 加入会话历史（使用 core.Session 接口方法，内部加锁）
		session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: durablePrompt})

		// 获取带历史的消息快照（已深拷贝，线程安全）
		messages := session.Snapshot()
		// 动态生成的群聊摘要与最近原始消息仅作为本轮瞬时上下文附加在 messages 快照中，
		// 不写入持久化的 session history，防止多轮对话中历史不断重复膨胀。
		if len(messages) > 0 {
			messages[len(messages)-1].Content = requestPrompt
		}

		sendHook := func(toolResultJSON string) error {
			var toolOutput struct {
				Messages []tools.Msg `json:"messages"`
			}
			if err := json.Unmarshal([]byte(toolResultJSON), &toolOutput); err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("SendHook: 解析 send_message 结果失败: %v", err))
				return fmt.Errorf("解析 send_message 结果失败: %w", err)
			}
			oneBotSegments := tools.BuildOneBotMessage(toolOutput.Messages)
			if len(oneBotSegments) == 0 {
				return fmt.Errorf("消息内容为空")
			}
			if event.MessageType == "group" {
				oneBotSegments = wrapGroupReply(oneBotSegments, event)
			}
			botAction := model.OneBotAction{
				Action: action,
				Params: map[string]any{
					type1:     id,
					"message": oneBotSegments,
				},
				Echo: echo,
			}
			ackResp, err := conn.SendActionAndWait(botAction, actionACKTimeout())
			if err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("SendHook: 消息发送未送达: action=%s retcode=%d err=%v", action, ackResp.RetCode, err))
				reason := strings.TrimSpace(ackResp.Wording)
				if reason == "" {
					reason = strings.TrimSpace(ackResp.Message)
				}
				if reason == "" && ackResp.RetCode != 0 {
					reason = fmt.Sprintf("retcode %d", ackResp.RetCode)
				}
				if reason == "" {
					reason = err.Error()
				}
				return fmt.Errorf("%s", reason)
			}
			return nil
		}

		runResult = engine.RunMessagesWithContext(messages, llm.RunContext{
			SessionID:     historyKey(event),
			Owner:         owner,
			OwnerType:     ownerType,
			SendHook:      sendHook,
			Billing:       billingState,
			RouteScope:    routeScope,
			RouteSnapshot: routeSnapshot,
		})
		replyText = runResult.Content

		// 计费回执处理与历史保护
		if billingState != nil && billingState.BillingActive {
			if runResult.Error != nil && billingState.IterationsBilled == 0 {
				// 首次迭代即失败且无任何消费记录，不污染历史并直接回复
				session.TrimHistory(len(session.Snapshot()) - 1)
				sendDirectReply(action, type1, id, echo, event, conn, runResult.Content)
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

		// A deliberate terminal silence keeps only the user turn in history. It
		// never emits an empty OneBot message or feeds the turn into automatic
		// memory extraction.
		if runResult.Silent {
			engine.TrimSession(session)
			logs.Info(logs.SYSTEM, fmt.Sprintf("本轮保持沉默: session=%s", historyKey(event)))
			return
		}
	} else {
		replyText = "系统出错，引擎未初始化"
		logs.Warn(logs.SYSTEM, "警告：未设置处理消息的 engine")
	}

	// 5. Prepare the final message for OneBot by parsing the engine's response
	var finalMessage any

	var toolOutput struct {
		Messages []tools.Msg `json:"messages"`
	}

	if err := json.Unmarshal([]byte(replyText), &toolOutput); err == nil && len(toolOutput.Messages) > 0 {
		// A. It's a tool call JSON
		logs.Debug(logs.WEBSOCKET, "解析工具调用 JSON 成功，准备组装富文本消息")
		oneBotSegments := tools.BuildOneBotMessage(toolOutput.Messages)
		if len(oneBotSegments) > 0 {
			if receiptText != "" {
				oneBotSegments = append(oneBotSegments, tools.OneBotSegment{
					Type: "text",
					Data: map[string]any{"text": "\n\n" + receiptText},
				})
			}
			if event.MessageType == "group" {
				oneBotSegments = wrapGroupReply(oneBotSegments, event)
			}
			finalMessage = oneBotSegments
		} else {
			displayText := replyText
			if receiptText != "" {
				displayText = displayText + "\n\n" + receiptText
			}
			finalMessage = displayText
		}
	} else {
		// B. It's plain text
		displayText := replyText
		if receiptText != "" {
			if strings.TrimSpace(displayText) == "" {
				displayText = receiptText
			} else {
				displayText = displayText + "\n\n" + receiptText
			}
		}

		if event.MessageType == "group" {
			// 群聊回复：按开关前置 reply 段（引用原消息）与 at 段
			enableAt := os.Getenv("ENABLE_AT_IN_GROUP_MSG") == "true"
			enableReply := os.Getenv("ENABLE_REPLY_IN_GROUP_MSG") == "true"
			if enableAt || enableReply {
				textSeg := tools.OneBotSegment{
					Type: "text",
					Data: map[string]any{"text": " " + displayText},
				}
				finalMessage = wrapGroupReply([]tools.OneBotSegment{textSeg}, event)
			} else {
				finalMessage = displayText
			}

		} else {
			// Just plain text for private messages
			finalMessage = displayText
		}
	}

	// 6. Build and send the final OneBot Action, waiting for platform ACK response
	botAction := model.OneBotAction{
		Action: action,
		Params: map[string]any{
			type1:     id,
			"message": finalMessage, // Use the processed finalMessage
		},
		Echo: echo,
	}

	ackResp, err := conn.SendActionAndWait(botAction, actionACKTimeout())
	if err == nil {
		// 只有平台确认发送成功 (status == "ok", retcode == 0) 后才提交 assistant 历史与记忆
		if session != nil {
			session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: replyText})
			if engine != nil {
				engine.TrimSession(session)
			}
		}

		if event.MessageType == "group" && engine != nil && session != nil {
			botReply := extractBotReplyText(replyText)
			if strings.TrimSpace(botReply) != "" {
				botName := os.Getenv("BOT_NAME")
				if botName == "" {
					botName = defaultBotName
				}
				var maxBufferSize int
				if engine.GroupCompactor != nil {
					maxBufferSize = engine.GroupCompactor.MaxBufferSize()
				}
				session.AppendGroupCompactMessage(
					llm.GroupCompactMessage{
						Role:    "assistant",
						Sender:  botName,
						Content: strings.TrimSpace(botReply),
						Time:    time.Now().Format("15:04:05"),
					},
					maxBufferSize,
				)
				if engine.GroupCompactor != nil {
					owner, _ := memory.OwnerForGroup(event.GroupID)
					engine.GroupCompactor.TriggerWithScope(session, owner, routeScope)
				}
			}
		}

		if engine != nil && session != nil {
			if runResult.MemoryWritten {
				logs.Info(logs.SYSTEM, "本轮已通过 memory.write 处理记忆，跳过自动提取累计")
			} else if strings.TrimSpace(userText) != "" && strings.TrimSpace(replyText) != "" {
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
						Message:   core.ChatMessage{Role: core.RoleAssistant, Content: replyText},
					},
				})
			}
		}
	} else {
		// 平台发送失败 (retcode != 0 或超时或写入失败)：不记录 assistant history，不写入 compact buffer，不进入 memory extraction
		reason := strings.TrimSpace(ackResp.Wording)
		if reason == "" {
			reason = strings.TrimSpace(ackResp.Message)
		}
		if reason == "" && ackResp.RetCode != 0 {
			reason = fmt.Sprintf("retcode %d", ackResp.RetCode)
		}
		if reason == "" {
			reason = err.Error()
		}
		if session != nil {
			session.SetDeliveryFailure(llm.DeliveryFailure{
				Platform: "onebot",
				Action:   action,
				RetCode:  ackResp.RetCode,
				Message:  ackResp.Message,
				Wording:  reason,
			})
			if engine != nil {
				engine.TrimSession(session)
			}
		}
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("OneBot 消息未送达: action=%s retcode=%d reason=%s err=%v", action, ackResp.RetCode, reason, err))
	}
}

func sendDirectReply(action, type1, id, echo string, event model.OneBotEvent, conn *wsConnection, text string) {
	var finalMessage interface{}
	if event.MessageType == "group" {
		enableAt := os.Getenv("ENABLE_AT_IN_GROUP_MSG") == "true"
		enableReply := os.Getenv("ENABLE_REPLY_IN_GROUP_MSG") == "true"
		if enableAt || enableReply {
			textSeg := tools.OneBotSegment{
				Type: "text",
				Data: map[string]interface{}{"text": " " + text},
			}
			finalMessage = wrapGroupReply([]tools.OneBotSegment{textSeg}, event)
		} else {
			finalMessage = text
		}
	} else {
		finalMessage = text
	}

	botAction := model.OneBotAction{
		Action: action,
		Params: map[string]interface{}{
			type1:     id,
			"message": finalMessage,
		},
		Echo: echo,
	}

	actionBytes, _ := json.Marshal(botAction)
	if err := conn.WriteMessage(websocket.TextMessage, actionBytes); err != nil {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("发送直接回复失败: %v", err))
	}
}

func captureGroupCompactMessage(event model.OneBotEvent, engine *llm.Engine) {
	if engine == nil || event.GroupID <= 0 {
		return
	}
	segments := ParseMessageSegments(event.Message)
	visibleText := extractUserText(segments, event.Message)
	if strings.TrimSpace(visibleText) == "" {
		return
	}
	session := engine.SessionManager.GetOrCreate(historyKey(event))
	var msgID string
	if event.MessageID != 0 {
		msgID = strconv.FormatInt(int64(event.MessageID), 10)
	}
	var maxBufferSize int
	if engine.GroupCompactor != nil {
		maxBufferSize = engine.GroupCompactor.MaxBufferSize()
	}
	session.AppendGroupCompactMessage(
		llm.GroupCompactMessage{
			Role:      "user",
			Sender:    senderDisplayName(event),
			SenderID:  strconv.FormatInt(event.UserID, 10),
			Content:   strings.TrimSpace(visibleText),
			MessageID: msgID,
			Time:      time.Now().Format("15:04:05"),
		},
		maxBufferSize,
	)
	if engine.GroupCompactor != nil {
		owner, _ := memory.OwnerForGroup(event.GroupID)
		engine.GroupCompactor.TriggerWithScope(session, owner, oneBotRouteScope(event))
	}
}

func formatGroupSpeakerMessage(event model.OneBotEvent, text string) string {
	return fmt.Sprintf("[user] %s (%d): %s", senderDisplayName(event), event.UserID, strings.TrimSpace(text))
}

func formatGroupAssistantMessage(botName, text string) string {
	if botName == "" {
		botName = defaultBotName
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

// wrapGroupReply 按 env 开关为群聊回复前置 reply 段（引用原消息）与 at 段。
// 顺序：reply → at → base；两个开关都关闭时返回 base 原样，方便无条件调用。
func wrapGroupReply(base []tools.OneBotSegment, event model.OneBotEvent) []tools.OneBotSegment {
	out := make([]tools.OneBotSegment, 0, len(base)+2)
	if os.Getenv("ENABLE_REPLY_IN_GROUP_MSG") == "true" {
		out = append(out, tools.OneBotSegment{
			Type: "reply",
			Data: map[string]interface{}{"id": strconv.FormatInt(int64(event.MessageID), 10)},
		})
	}
	if os.Getenv("ENABLE_AT_IN_GROUP_MSG") == "true" {
		out = append(out, tools.OneBotSegment{
			Type: "at",
			Data: map[string]interface{}{"qq": strconv.FormatInt(event.UserID, 10)},
		})
	}
	if len(out) == 0 {
		return base
	}
	return append(out, base...)
}

func buildChatMessagesFromEvent(event model.OneBotEvent, engine *llm.Engine) []llm.ChatMessage {
	raws := EventRawMessages(event)
	messages := make([]llm.ChatMessage, 0, len(raws))

	for _, raw := range raws {
		segments := ParseMessageSegments(raw)
		userText := extractUserText(segments, raw)
		if content.IsContainImage(segments) {
			provider := engine.VisionProvider
			if provider == nil {
				provider = engine.Provider
			}
			scope := oneBotRouteScope(event)
			imageDesc := content.ProcessImage(context.Background(), segments, provider, core.RouteContext{Platform: scope.Platform, GroupID: scope.GroupID})
			userText = strings.TrimSpace(userText + " 【图片内容】：" + imageDesc)
		}
		messages = append(messages, llm.ChatMessage{Role: "user", Content: userText})
	}

	return messages
}

func historyKey(event model.OneBotEvent) string {
	if event.MessageType == "group" {
		return fmt.Sprintf("group:%d", event.GroupID)
	}
	return fmt.Sprintf("private:%d", event.UserID)
}

func oneBotRouteScope(event model.OneBotEvent) modelrouter.Scope {
	scope := modelrouter.Scope{Platform: "onebot"}
	if event.MessageType == "group" && event.GroupID != 0 {
		scope.GroupID = strconv.FormatInt(event.GroupID, 10)
	}
	return scope
}

// extractUserText 从消息段中提取纯文本内容
func extractUserText(segments []content.MessageSegment, raw json.RawMessage) string {
	var texts []string

	for _, seg := range segments {
		switch seg.Type {
		case "text":
			if text, ok := seg.Data["text"].(string); ok {
				texts = append(texts, text)
			}
		case "at":
			texts = append(texts, fmt.Sprintf("[@%v] ", seg.Data["qq"]))
		case "face":
			texts = append(texts, fmt.Sprintf("[表情:%v] ", seg.Data["id"]))
		case "image":
			texts = append(texts, "[图片] ")
		case "record":
			texts = append(texts, "[语音] ")
		case "video":
			texts = append(texts, "[视频] ")
		case "file":
			name := seg.Data["name"]
			if name == nil {
				name = seg.Data["file"]
			}
			texts = append(texts, fmt.Sprintf("[文件:%v] ", name))
		case "reply":
			texts = append(texts, fmt.Sprintf("[回复:%v] ", seg.Data["id"]))
		case "location":
			texts = append(texts, fmt.Sprintf("[位置:%v,%v %v] ", seg.Data["lat"], seg.Data["lon"], seg.Data["title"]))
		case "json", "xml":
			// 先尝试获取 data 字段
			data := seg.Data["data"]
			// 如果 data 是 map 或 slice，重新 marshal 成标准 JSON
			if b, err := json.Marshal(data); err == nil {
				texts = append(texts, fmt.Sprintf("[%s:%s]", seg.Type, string(b)))
			} else if s, ok := data.(string); ok {
				// 如果本来就是字符串，直接用
				texts = append(texts, fmt.Sprintf("[%s:%s]", seg.Type, s))
			}
		default:
			bytes, err := json.Marshal(seg)
			if err == nil {
				texts = append(texts, string(bytes))
			} else {
				texts = append(texts, "[未知消息段]")
				logs.Warn(logs.WEBSOCKET, fmt.Sprintf("Failed to marshal unknown segment: %v", err))
			}
		}
	}

	if len(texts) == 0 {
		var rawText string
		if err := json.Unmarshal(raw, &rawText); err == nil {
			return rawText
		}
		return string(raw)
	}

	return strings.TrimSpace(strings.Join(texts, ""))
}
