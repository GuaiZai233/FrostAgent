package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/llm"
	"encoding/json"
	"log"
	"net/http"
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

var writeMu sync.Mutex

func HandleWS(engine *llm.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket 升级失败: %v\n", err)
			return
		}

		log.Println("WebSocket 连接已建立: ", r.RemoteAddr)

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("读取消息失败: %v\n", err)
				conn.Close()
				break
			}

			var event model.OneBotEvent
			if err := json.Unmarshal(message, &event); err != nil {
				log.Printf("消息解析失败: %v\n", err)
				continue
			}

			// filter heartbeat pkg
			if event.MetaEventType == "heartbeat" {
				continue
			}

			go processEvent(conn, event, engine)
		}
	}

}

// processEvent process particular event, and dispatch agent and middleware
func processEvent(conn *websocket.Conn, event model.OneBotEvent, engine *llm.Engine) {
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

func reply(action string, type1 string, id string, echo string, event model.OneBotEvent, engine *llm.Engine, conn *websocket.Conn) {
	replyText := "系统出错，暂无法处理"

	if engine == nil {
		log.Println("警告：未设置处理消息的 engine")
	} else {
		replyText = engine.RunMessages(buildChatMessagesFromEvent(event, engine))
	}

	botAction := model.OneBotAction{
		Action: action,
		Params: map[string]interface{}{
			type1:     id,
			"message": replyText,
		},
		Echo: echo,
	}

	actionBytes, _ := json.Marshal(botAction)
	writeMu.Lock()
	err := conn.WriteMessage(websocket.TextMessage, actionBytes)
	writeMu.Unlock()
	if err != nil {
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
