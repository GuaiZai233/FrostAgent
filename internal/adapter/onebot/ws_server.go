package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/tools"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func newWSConnection(conn *websocket.Conn) *wsConnection {
	return &wsConnection{conn: conn}
}

func (c *wsConnection) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

func (c *wsConnection) Close() error {
	return c.conn.Close()
}

func (c *wsConnection) WriteControl(messageType int, data []byte, deadline time.Time) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(messageType, data, deadline)
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

		stopHeartbeat := startHeartbeat(wsConn)
		defer stopHeartbeat()
		conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(70 * time.Second))
			return nil
		})

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("读取消息失败: %v", err))
				break
			}

			var event model.OneBotEvent
			if err := json.Unmarshal(message, &event); err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("消息解析失败: %v", err))
				continue
			}

			if event.MetaEventType == "heartbeat" {
				continue
			}

			go processEvent(wsConn, event, engine)
		}
	}
}



func startHeartbeat(conn *wsConnection) func() {
	stop := make(chan struct{})
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				deadline := time.Now().Add(10 * time.Second)
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), deadline); err != nil {
					log.Printf("WebSocket 心跳发送失败: %v", err)
					return
				}
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

func processEvent(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine) {
	if event.PostType != "message" {
		return
	}

	if event.MessageType == "group" {
		logs.Info(logs.WEBSOCKET, fmt.Sprintf("收到群 [%d] 用户 [%d] 的消息: %s", event.GroupID, event.UserID, string(event.Message)))
		if !IsMentionedBot(event) {
			return
		}
		reply("send_group_msg", "group_id", strconv.FormatInt(event.GroupID, 10), "echo_agent_req_001", event, engine, conn)

	} else if event.MessageType == "private" {
		logs.Info(logs.WEBSOCKET, fmt.Sprintf("收到用户 [%d] 的私聊消息: %s", event.UserID, string(event.Message)))
		reply("send_private_msg", "user_id", strconv.FormatInt(event.UserID, 10), "echo_private_001", event, engine, conn)
	}
}

func reply(action string, type1 string, id string, echo string, event model.OneBotEvent, engine *llm.Engine, conn *wsConnection) {
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
	}
	contextBytes, _ := json.Marshal(contextMap)

	// 3. Combine user text and context into the final prompt for the LLM
	prompt := fmt.Sprintf("User Message: %s\n\n<system_context>\n%s\n</system_context>", userText, string(contextBytes))

	// 4. Call the agent engine with history
	var replyText string
	if engine != nil {
		sessionID := historyKey(event)
		session := engine.SessionManager.GetOrCreate(sessionID)

		// 将用户的 prompt 加入会话历史（使用 core.Session 接口方法，内部加锁）
		session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: prompt})

		// 获取带历史的消息快照（已深拷贝，线程安全）
		messages := session.Snapshot()

		// 设置 SendHook：send_message 调用时立即通过 OneBot 发送
		engine.SendHook = func(toolResultJSON string) {
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

		// 传递给大模型（不在锁内调用，避免阻塞历史读写）
		replyText = engine.RunMessages(messages)
		engine.SendHook = nil

		// 将大模型的回复也加入会话历史
		session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: replyText})
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
			finalMessage = oneBotSegments
		} else {
			finalMessage = replyText // Fallback to raw text if conversion fails
		}
	} else {
		// B. It's plain text
		if event.MessageType == "group" {
			// Prepend @ in group chats
			if os.Getenv("ENABLE_AT_IN_GROUP_MSG") == "true" {
				finalMessage = []map[string]interface{}{
					{"type": "at", "data": map[string]interface{}{"qq": strconv.FormatInt(event.UserID, 10)}},
					{"type": "text", "data": map[string]interface{}{"text": " " + replyText}},
				}
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
