package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/modelrouter"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Adapter 实现 core.MessageAdapter 接口，管理 AstrBot WebSocket 客户端连接与消息收发。
type Adapter struct {
	engine *llm.Engine
	mu     sync.RWMutex
	conns  map[*wsConn]struct{}
}

// NewAdapter 创建一个新的 AstrBot 适配器实例。
func NewAdapter(engine *llm.Engine) *Adapter {
	return &Adapter{
		engine: engine,
		conns:  make(map[*wsConn]struct{}),
	}
}

// ID 返回平台唯一标识 "astrbot"
func (a *Adapter) ID() string {
	return "astrbot"
}

func (a *Adapter) registerConn(c *wsConn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.conns[c] = struct{}{}
}

func (a *Adapter) unregisterConn(c *wsConn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.conns, c)
}

// Send 将 core.OutgoingMessage 转换为 AstrBot Action 并发送到活跃连接。
func (a *Adapter) Send(ctx context.Context, msg core.OutgoingMessage) error {
	a.mu.RLock()
	conns := make([]*wsConn, 0, len(a.conns))
	for c := range a.conns {
		conns = append(conns, c)
	}
	a.mu.RUnlock()

	if len(conns) == 0 {
		return fmt.Errorf("astrbot: 没有可用的活跃 WebSocket 连接")
	}

	groupID := ""
	userID := ""
	if msg.MessageType == "group" {
		groupID = msg.TargetID
	} else {
		userID = msg.TargetID
	}

	action := Action{
		Type:           "action",
		Action:         "send_message",
		TargetID:       msg.TargetID,
		MessageType:    msg.MessageType,
		GroupID:        groupID,
		UserID:         userID,
		Content:        msg.Content,
		Attachments:    msg.Attachments,
		IsIntermediate: false,
		Echo:           fmt.Sprintf("astrbot_send_%s", msg.TargetID),
	}

	data, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("astrbot: 序列化 action 失败: %w", err)
	}

	var errs []error
	for _, c := range conns {
		if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
			logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot Adapter Send 失败: %v", err))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ToIncomingMessage 将 AstrBot Event 转换为统一的 core.IncomingMessage。
func ToIncomingMessage(event Event) core.IncomingMessage {
	createdAt := time.Now()
	if event.Timestamp > 0 {
		createdAt = time.Unix(event.Timestamp, 0)
	}

	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}

	return core.IncomingMessage{
		ID:          event.MessageID,
		SessionID:   event.SessionID,
		UserID:      event.UserID,
		SenderName:  event.SenderName,
		SenderCard:  event.SenderCard,
		Content:     event.Content,
		Platform:    platform,
		MessageType: event.MessageType,
		GroupID:     event.GroupID,
		GroupName:   event.GroupName,
		CreatedAt:   createdAt,
		Metadata:    event.Metadata,
		Attachments: event.Attachments,
		RawMessage:  event,
	}
}

// Handler 返回用于注册到 HTTP mux 的 WebSocket Handler。
func (a *Adapter) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot WebSocket 升级失败: %v", err))
			return
		}
		c := newWSConn(conn)
		a.registerConn(c)
		defer func() {
			a.unregisterConn(c)
			c.Close()
		}()

		logs.Info(logs.WEBSOCKET, fmt.Sprintf("AstrBot WebSocket 连接已建立: %s", r.RemoteAddr))

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot 读取消息失败: %v", err))
				break
			}

			var event Event
			if err := json.Unmarshal(message, &event); err != nil {
				logs.Error(logs.WEBSOCKET, fmt.Sprintf("AstrBot 消息解析失败: %v", err))
				continue
			}

			if event.Type == "heartbeat" || event.EventType == "heartbeat" {
				continue
			}

			var routeSnapshot *modelrouter.Snapshot
			if a.engine != nil && a.engine.ModelRouter != nil &&
				(event.MessageType == "group" || event.MessageType == "private") {
				routeSnapshot = a.engine.ModelRouter.Snapshot()
				if routeSnapshot.IsDisabled(modelrouter.WorkloadDialogue, astrBotRouteScope(event)) {
					continue
				}
			}

			if event.MessageType == "group" {
				captureGroupCompactMessage(event, a.engine)
			}

			var turn *llm.SessionTurn
			if a.engine != nil && a.engine.SessionManager != nil &&
				(event.MessageType == "group" || event.MessageType == "private") {
				turn = a.engine.SessionManager.GetOrCreate(sessionKey(event)).ReserveTurn()
			}
			go processEvent(c, event, a.engine, turn, routeSnapshot)
		}
	}
}
