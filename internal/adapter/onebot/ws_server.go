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
		// defer conn.Close() is handled below if needed, but it's okay here

		log.Println("WebSocket 连接已建立: ", r.RemoteAddr)

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("读取消息失败: %v\n", err)
				conn.Close()
				break
			}

			// debug: print raw msg from llonebot
			/*
				log.Println("收到原始数据: ", string(message))

				var event model.OneBotEvent
				if err := json.Unmarshal(message, &event); err != nil {
					log.Println("解析事件失败:", err)
					continue
				}
			*/
			var event model.OneBotEvent
			if err := json.Unmarshal(message, &event); err != nil {
				log.Printf("消息解析失败: %v\n", err)
				continue
			}

			//filter heartbeat pkg
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
	// 解析消息段
	var segments []content.MessageSegment
	if err := json.Unmarshal(event.Message, &segments); err != nil {
		log.Printf("解析消息段失败: %v\n", err)
		segments = []content.MessageSegment{}
	}
	//init reply text
	replyText := "系统出错，暂无法处理"

	//containing img or not
	if content.IsContainImage(segments) {
		imageDesc := content.ProcessImage(segments, engine.LLMClient, engine.BaseURL, engine.APIKey, engine.ModelName)
		replyText = engine.Run(extractUserText(segments, event) + " 【多模态模型解析】用户图片内容概括：" + imageDesc)
	} else if engine != nil {
		replyText = engine.Run(extractUserText(segments, event))
	} else {
		log.Println("警告：未设置处理消息的 engine")
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

// extractUserText 从消息段中提取纯文本内容
func extractUserText(segments []content.MessageSegment, event model.OneBotEvent) string {
	var texts []string

	for _, seg := range segments {
		if seg.Type == "text" {
			if text, ok := seg.Data["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}

	// 兜底：如果没有解析到文本，返回原始消息
	if len(texts) == 0 {
		return string(event.Message)
	}

	// 使用 strings.Join 拼接多个文本段
	return strings.Join(texts, "")
}
