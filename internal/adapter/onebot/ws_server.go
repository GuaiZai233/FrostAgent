package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/tools"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

// 生产环境必须限制 Origin，目前仅用于本地调试
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var chatHistory = newMessageHistory(historyLimitFromEnv())

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

func HandleWS(engine *llm.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket 升级失败: %v\n", err)
			return
		}
		wsConn := newWSConnection(conn)
		defer wsConn.Close()

		log.Println("WebSocket 连接已建立: ", r.RemoteAddr)

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("读取消息失败: %v\n", err)
				break
			}

			var event model.OneBotEvent
			if err := json.Unmarshal(message, &event); err != nil {
				log.Printf("消息解析失败: %v\n", err)
				continue
			}

			if event.MetaEventType == "heartbeat" {
				continue
			}

			go processEvent(wsConn, event, engine)
		}
	}
}

func processEvent(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine) {
	if event.PostType != "message" {
		return
	}

	if event.MessageType == "group" {
		log.Printf("收到群 [%d] 用户 [%d] 的消息: %s", event.GroupID, event.UserID, string(event.Message))
		if !IsMentionedBot(event) {
			return
		}
		reply("send_group_msg", "group_id", strconv.FormatInt(event.GroupID, 10), "echo_agent_req_001", event, engine, conn)

	} else if event.MessageType == "private" {
		log.Printf("收到用户 [%d] 的私聊消息: %s", event.UserID, string(event.Message))
		reply("send_private_msg", "user_id", strconv.FormatInt(event.UserID, 10), "echo_private_001", event, engine, conn)
	}
}

func reply(action string, type1 string, id string, echo string, event model.OneBotEvent, engine *llm.Engine, conn *wsConnection) {
	// 1. Build the prompt string for the LLM
	var segments []content.MessageSegment
	if err := json.Unmarshal(event.Message, &segments); err != nil {
		log.Printf("解析消息段失败: %v\n", err)
		// Don't return, just work with an empty segment list
	}

	prompt := extractUserText(segments, event.Message)
	if content.IsContainImage(segments) {
		imageDesc := content.ProcessImage(segments, engine.LLMClient, engine.BaseURL, engine.APIKey, engine.ModelName)
		prompt = prompt + " 【图片内容】：" + imageDesc
	}

	// 2. Call the agent engine
	var replyText string
	if engine != nil {
		// Here we use the Run method which takes a simple string prompt
		replyText = engine.Run(prompt)
	} else {
		replyText = "系统出错，引擎未初始化"
		log.Println("警告：未设置处理消息的 engine")
	}

	// 3. Prepare the final message for OneBot by parsing the engine's response
	var finalMessage interface{}

	var toolOutput struct {
		Messages []tools.Msg `json:"messages"`
	}

	if err := json.Unmarshal([]byte(replyText), &toolOutput); err == nil && len(toolOutput.Messages) > 0 {
		// A. It's a tool call JSON
		log.Printf("解析工具调用 JSON 成功，准备组装富文本消息")
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
			finalMessage = []map[string]interface{}{
				{"type": "at", "data": map[string]interface{}{"qq": strconv.FormatInt(event.UserID, 10)}},
				{"type": "text", "data": map[string]interface{}{"text": " " + replyText}},
			}
		} else {
			// Just plain text for private messages
			finalMessage = replyText
		}
	}

	// 4. Build and send the final OneBot Action
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
		log.Printf("发送消息失败: %v\n", err)
	}
}

func buildChatMessagesFromEvent(event model.OneBotEvent, engine *llm.Engine) []llm.ChatMessage {
	raws := EventRawMessages(event)
	messages := make([]llm.ChatMessage, 0, len(raws))

	for _, raw := range raws {
		segments := ParseMessageSegments(raw)
		userText := extractUserText(segments, raw)
		if content.IsContainImage(segments) {
			imageDesc := content.ProcessImage(segments, engine.LLMClient, engine.BaseURL, engine.APIKey, engine.ModelName)
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

func historyLimitFromEnv() int {
	limit := llm.DefaultMaxMessages
	if value := strings.TrimSpace(os.Getenv("ONEBOT_CONTEXT_MESSAGES")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	return limit
}

// extractUserText 从消息段中提取纯文本内容
func extractUserText(segments []content.MessageSegment, raw json.RawMessage) string {
	var texts []string

	for _, seg := range segments {
		if seg.Type == "text" {
			if text, ok := seg.Data["text"].(string); ok {
				texts = append(texts, text)
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
