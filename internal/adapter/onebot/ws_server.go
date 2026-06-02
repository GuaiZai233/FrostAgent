package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/tools"
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
		defer conn.Close()

		log.Println("WebSocket 连接已建立: ", r.RemoteAddr)

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("读取消息失败: %v\n", err)
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

	// 1. 准备给大模型的输入
	var replyText_temp string
	if content.IsContainImage(segments) {
		imageDesc := content.ProcessImage(segments, engine.LLMClient, engine.BaseURL, engine.APIKey, engine.ModelName)
		replyText_temp = extractUserText(segments, event) + " 【图片内容】：" + imageDesc
	} else if engine != nil {
		replyText_temp = extractUserText(segments, event)
	} else {
		log.Println("警告：未设置处理消息的 engine")
		replyText_temp = "系统错误" // 设置默认值以防万一
	}

	// 2. 调用大模型
	replyText = "系统出错，暂无法处理" // 大模型调用失败时的默认回复
	if engine != nil {
		replyText = engine.Run(replyText_temp)
	}

	// 3. 准备最终要发送给 OneBot 的消息 (可能是字符串，也可能是数组)
	var finalMessage interface{}

	// 尝试将 LLM 的返回解析为工具的 JSON 输出
	var toolOutput struct {
		Messages []tools.Msg `json:"messages"`
	}

	if err := json.Unmarshal([]byte(replyText), &toolOutput); err == nil && len(toolOutput.Messages) > 0 {
		// A. 如果解析成功，说明是工具调用的结果，调用已有的工具函数将其转换为 OneBot 格式
		log.Printf("解析工具调用 JSON 成功，准备组装富文本消息")

		// 调用 tools 包中的 BuildOneBotMessage 方法
		oneBotSegments := tools.BuildOneBotMessage(toolOutput.Messages)

		if len(oneBotSegments) > 0 {
			finalMessage = oneBotSegments
		} else {
			// 如果返回的 segments 是空，则退回纯文本
			finalMessage = replyText
		}
	} else {
		// B. 如果解析失败，说明 LLM 返回的是纯文本
		if event.MessageType == "group" {
			// 在群聊中，为纯文本回复自动加上 @
			finalMessage = []map[string]interface{}{
				{
					"type": "at",
					"data": map[string]interface{}{"qq": strconv.FormatInt(event.UserID, 10)},
				},
				{
					"type": "text",
					"data": map[string]interface{}{"text": " " + replyText},
				},
			}
		} else {
			// 私聊中，直接发送纯文本
			finalMessage = replyText
		}
	}

	// 4. 构建并发送最终的 OneBot Action
	botAction := model.OneBotAction{
		Action: action,
		Params: map[string]interface{}{
			type1:     id,
			"message": finalMessage,
		},
		Echo: echo,
	}

	actionBytes, _ := json.Marshal(botAction)
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := conn.WriteMessage(websocket.TextMessage, actionBytes); err != nil {
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
