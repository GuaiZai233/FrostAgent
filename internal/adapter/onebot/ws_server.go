package onebot

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"FrostAgent/internal/agent"
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

func HandleWS(engine *agent.Engine) http.HandlerFunc {
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

func processEvent(conn *websocket.Conn, event model.OneBotEvent, engine *agent.Engine) {
	if event.PostType == "message" {
		if event.MessageType == "group" {
			log.Printf("收到群 [%d] 用户 [%d] 的消息: %s", event.GroupID, event.UserID, string(event.Message))

			// 1. 此处可接入 middleware 进行前置处理（如防刷屏限流）
			// 2. 此处可调用 internal/agent 进行大模型或工作流编排

			//replyText := "收到消息: " + string(event.Message)

			action := model.OneBotAction{
				Action: "send_group_msg",
				Params: map[string]interface{}{
					"group_id": event.GroupID,
					//"message":  replyText,
				},
				Echo: "echo_agent_req_001", // 可选字段，用于关联请求和响应，方便调试和日志追踪
			}

			actionBytes, _ := json.Marshal(action)

			writeMu.Lock()
			err := conn.WriteMessage(websocket.TextMessage, actionBytes)
			writeMu.Unlock()
			if err != nil {
				log.Printf("发送消息失败: %v\n", err)
			}

		} else if event.MessageType == "private" {
			log.Printf("收到用户 [%d] 的私聊消息: %s", event.UserID, string(event.Message))

			replyText := "系统出错，暂无法处理"
			if engine != nil {
				replyText = engine.Run(string(event.Message))
			} else {
				log.Println("警告：未设置处理消息的 engine")
			}

			action := model.OneBotAction{
				Action: "send_private_msg",
				Params: map[string]interface{}{
					"user_id": event.UserID,
					"message": replyText,
				},
				Echo: "echo_private_001",
			}

			actionBytes, _ := json.Marshal(action)
			writeMu.Lock()
			err := conn.WriteMessage(websocket.TextMessage, actionBytes)
			writeMu.Unlock()
			if err != nil {
				log.Printf("发送消息失败: %v\n", err)
			}

		}
	}
}
