package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/tools"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"FrostAgent/internal/model"
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

func HandleWS(engine *llm.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logs.Error(logs.WEBSOCKET, fmt.Sprintf("WebSocket 升级失败: %v", err))
			return
		}
		wsConn := newWSConnection(conn)
		defer wsConn.Close()

		logs.Info(logs.WEBSOCKET, fmt.Sprintf("WebSocket 连接已建立: %s", r.RemoteAddr))

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("读取消息失败: %v", err))
				break
			}

			if wsConn.handleAPIResponse(message) {
				continue
			}

			var event model.OneBotEvent
			if err := json.Unmarshal(message, &event); err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("消息解析失败: %v", err))
				continue
			}

			if event.MetaEventType == "heartbeat" {
				continue
			}

			// Capture group context on the WebSocket read goroutine so messages
			// enter the compact ring in wire order, including non-mention traffic.
			if event.PostType == "message" && event.MessageType == "group" {
				captureGroupCompactMessage(event, engine)
			}
			var turn *llm.SessionTurn
			if engine != nil && engine.SessionManager != nil && event.PostType == "message" &&
				(event.MessageType == "group" || event.MessageType == "private") {
				turn = engine.SessionManager.GetOrCreate(historyKey(event)).ReserveTurn()
			}
			go processEvent(wsConn, event, engine, turn)
		}
	}
}

// processEvent holds its reserved session turn until routing and reply finish.
func processEvent(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine, turn *llm.SessionTurn) {
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
		reply("send_group_msg", "group_id", strconv.FormatInt(event.GroupID, 10), "echo_agent_req_001", event, engine, conn, replyContext.Prompt, responseContext)

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
		reply("send_private_msg", "user_id", strconv.FormatInt(event.UserID, 10), "echo_private_001", event, engine, conn, replyContext.Prompt, responseContext)
	}
}

// reply records terminal silence without sending or batching memory.
func reply(action string, type1 string, id string, echo string, event model.OneBotEvent, engine *llm.Engine, conn *wsConnection, replyContext string, responseContext string) {
	// 1. Extract user's visible message
	var segments []content.MessageSegment
	segments = []content.MessageSegment{}
	if err := json.Unmarshal(event.Message, &segments); err != nil {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("解析消息段失败: %v", err))
		// Don't return, just work with an empty segment list
	}

	userText := extractUserText(segments, event.Message)
	if content.IsContainImage(segments) {
		imageDesc := content.ProcessImage(segments, engine.Provider, engine.BaseURL, engine.APIKey, engine.ModelName)
		userText = userText + " 【图片内容】：" + imageDesc
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
	runningSummary := ""
	if engine != nil {
		session = engine.SessionManager.GetOrCreate(historyKey(event))
		if event.MessageType == "group" {
			runningSummary = session.GroupRunningSummary()
		}
	}

	// 3. Combine user text, the group running summary, and transport context.
	// The summary intentionally stays in the user segment rather than system.
	prompt := fmt.Sprintf("User Message: %s", userText)
	if runningSummary != "" {
		prompt += fmt.Sprintf(
			"\n\n<group_running_summary>\n%s\n</group_running_summary>",
			runningSummary,
		)
	}
	if responseContext != "" {
		prompt += fmt.Sprintf("\n\n<response_context>\n%s\n</response_context>", responseContext)
	}
	prompt += fmt.Sprintf("\n\n<system_context>\n%s\n</system_context>", string(contextBytes))
	requestPrompt := prompt
	if replyContext != "" {
		requestPrompt += fmt.Sprintf("\n\n<reply_context>\n%s\n</reply_context>", replyContext)
	}

	// 4. Call the agent engine with history
	var replyText string
	if engine != nil {
		// 将用户的 prompt 加入会话历史（使用 core.Session 接口方法，内部加锁）
		session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: prompt})

		// 获取带历史的消息快照（已深拷贝，线程安全）
		messages := session.Snapshot()
		// 引用原文仅供本轮理解，不写回 session，避免在后续 history、
		// running compact 与自动记忆中重复累计旧消息。
		if replyContext != "" && len(messages) > 0 {
			messages[len(messages)-1].Content = requestPrompt
		}

		sendHook := func(toolResultJSON string) {
			var toolOutput struct {
				Messages []tools.Msg `json:"messages"`
			}
			if err := json.Unmarshal([]byte(toolResultJSON), &toolOutput); err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("SendHook: 解析 send_message 结果失败: %v", err))
				return
			}
			oneBotSegments := tools.BuildOneBotMessage(toolOutput.Messages)
			if len(oneBotSegments) == 0 {
				return
			}
			if event.MessageType == "group" {
				oneBotSegments = wrapGroupReply(oneBotSegments, event)
			}
			botAction := model.OneBotAction{
				Action: action,
				Params: map[string]interface{}{
					type1:     id,
					"message": oneBotSegments,
				},
				Echo: echo,
			}
			actionBytes, _ := json.Marshal(botAction)
			if err := conn.WriteMessage(websocket.TextMessage, actionBytes); err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("SendHook: 发送消息失败: %v", err))
			}
		}

		// 传递给大模型（带 owner 隔离的记忆上下文）
		owner, ownerType := memory.OwnerForPrivate(strconv.FormatInt(event.UserID, 10))
		if event.MessageType == "group" {
			owner, ownerType = memory.OwnerForGroup(event.GroupID)
		}
		runResult := engine.RunMessagesWithContext(messages, llm.RunContext{
			Owner:     owner,
			OwnerType: ownerType,
			SendHook:  sendHook,
		})
		replyText = runResult.Content

		// A deliberate terminal silence keeps the user turn and an assistant
		// marker in history, but never emits an empty OneBot message or feeds the
		// turn into automatic memory extraction.
		if runResult.Silent {
			session.AddMessage(core.ChatMessage{
				Role:    core.RoleAssistant,
				Content: llm.AssistantSilentMarker,
			})
			engine.TrimSession(session)
			logs.Info(logs.SYSTEM, fmt.Sprintf("本轮保持沉默: session=%s", historyKey(event)))
			return
		}

		// 将大模型的回复也加入会话历史
		session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: replyText})
		// 裁剪会话历史，限制后续发送给 LLM 的上下文大小
		engine.TrimSession(session)

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
		replyText = "系统出错，引擎未初始化"
		logs.Warn(logs.SYSTEM, "警告：未设置处理消息的 engine")
	}

	// 5. Prepare the final message for OneBot by parsing the engine's response
	var finalMessage interface{}

	var toolOutput struct {
		Messages []tools.Msg `json:"messages"`
	}

	if err := json.Unmarshal([]byte(replyText), &toolOutput); err == nil && len(toolOutput.Messages) > 0 {
		// A. It's a tool call JSON
		logs.Debug(logs.WEBSOCKET, "解析工具调用 JSON 成功，准备组装富文本消息")
		oneBotSegments := tools.BuildOneBotMessage(toolOutput.Messages)
		if len(oneBotSegments) > 0 {
			if event.MessageType == "group" {
				oneBotSegments = wrapGroupReply(oneBotSegments, event)
			}
			finalMessage = oneBotSegments
		} else {
			finalMessage = replyText // Fallback to raw text if conversion fails
		}
	} else {
		// B. It's plain text
		if event.MessageType == "group" {
			// 群聊回复：按开关前置 reply 段（引用原消息）与 at 段
			enableAt := os.Getenv("ENABLE_AT_IN_GROUP_MSG") == "true"
			enableReply := os.Getenv("ENABLE_REPLY_IN_GROUP_MSG") == "true"
			if enableAt || enableReply {
				textSeg := tools.OneBotSegment{
					Type: "text",
					Data: map[string]interface{}{"text": " " + replyText},
				}
				finalMessage = wrapGroupReply([]tools.OneBotSegment{textSeg}, event)
			} else {
				finalMessage = replyText
			}

		} else {
			// Just plain text for private messages
			finalMessage = replyText
		}
	}

	// 6. Build and send the final OneBot Action
	botAction := model.OneBotAction{
		Action: action,
		Params: map[string]interface{}{
			type1:     id,
			"message": finalMessage, // Use the processed finalMessage
		},
		Echo: echo,
	}

	actionBytes, _ := json.Marshal(botAction)
	if err := conn.WriteMessage(websocket.TextMessage, actionBytes); err != nil {
		logs.Error(logs.WEBSOCKET, fmt.Sprintf("发送消息失败: %v", err))
	}
}

func captureGroupCompactMessage(event model.OneBotEvent, engine *llm.Engine) {
	if engine == nil || engine.GroupCompactor == nil || event.GroupID <= 0 {
		return
	}
	segments := ParseMessageSegments(event.Message)
	visibleText := extractUserText(segments, event.Message)
	if strings.TrimSpace(visibleText) == "" {
		return
	}
	session := engine.SessionManager.GetOrCreate(historyKey(event))
	session.AppendGroupCompactMessage(
		formatGroupSpeakerMessage(event, visibleText),
		engine.GroupCompactor.BufferSize(),
	)
	owner, _ := memory.OwnerForGroup(event.GroupID)
	engine.GroupCompactor.Trigger(session, owner)
}

func formatGroupSpeakerMessage(event model.OneBotEvent, text string) string {
	return fmt.Sprintf("%s (%d): %s", senderDisplayName(event), event.UserID, strings.TrimSpace(text))
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
			imageDesc := content.ProcessImage(segments, engine.Provider, engine.BaseURL, engine.APIKey, engine.ModelName)
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
