package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Adapter 实现 core.MessageAdapter 接口，管理 OneBot WebSocket 连接与消息收发。
type Adapter struct {
	engine  *llm.Engine
	stealer *sticker.Stealer
	mu      sync.RWMutex
	conns   map[*wsConnection]struct{}
}

// NewAdapter 创建一个新的 OneBot 适配器实例。
func NewAdapter(engine *llm.Engine) *Adapter {
	return &Adapter{
		engine: engine,
		conns:  make(map[*wsConnection]struct{}),
	}
}

func (a *Adapter) SetStealer(s *sticker.Stealer) {
	a.stealer = s
}

func (a *Adapter) observeStickers(event model.OneBotEvent) {
	if a.stealer == nil || event.PostType != "message" ||
		(event.MessageType != "group" && event.MessageType != "private") {
		return
	}
	messageID := strconv.FormatInt(int64(event.MessageID), 10)
	observeStickerSources(
		a.stealer,
		historyKey(event),
		messageID,
		stickerSourcesFromSegments(ParseMessageSegments(event.Message)),
		event.MessageType == "group",
	)
}

// ID 返回平台唯一标识 "onebot"
func (a *Adapter) ID() string {
	return "onebot"
}

func (a *Adapter) registerConn(c *wsConnection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.conns[c] = struct{}{}
}

func (a *Adapter) unregisterConn(c *wsConnection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.conns, c)
}

// Send 将 core.OutgoingMessage 转换为 OneBot Action 并发送到活跃连接。
// 注意：在当前 Stage 1 阶段，若存在多个活跃 OneBot 连接且未在 Metadata 中指定连接标识，
// Send 将向所有活跃连接发送以确保送达；未来多实例场景可通过 Metadata["self_id"] 等精细路由。
func (a *Adapter) Send(ctx context.Context, msg core.OutgoingMessage) error {
	a.mu.RLock()
	conns := make([]*wsConnection, 0, len(a.conns))
	for c := range a.conns {
		conns = append(conns, c)
	}
	a.mu.RUnlock()

	if len(conns) == 0 {
		return fmt.Errorf("onebot: 没有可用的活跃 WebSocket 连接")
	}

	actionName := "send_private_msg"
	idKey := "user_id"
	if msg.MessageType == "group" {
		actionName = "send_group_msg"
		idKey = "group_id"
	}

	// 构造消息段，支持文本与多媒体类型（image, record/audio, video）
	var segments []tools.OneBotSegment
	if msg.Content != "" {
		segments = append(segments, tools.OneBotSegment{
			Type: "text",
			Data: map[string]any{"text": msg.Content},
		})
	}
	for _, att := range msg.Attachments {
		switch att.Type {
		case core.AttachmentTypeImage:
			if att.URL != "" {
				segments = append(segments, tools.OneBotSegment{
					Type: "image",
					Data: map[string]any{"file": att.URL},
				})
			}
		case core.AttachmentTypeAudio:
			if att.URL != "" {
				segments = append(segments, tools.OneBotSegment{
					Type: "record",
					Data: map[string]any{"file": att.URL},
				})
			}
		case core.AttachmentTypeVideo:
			if att.URL != "" {
				segments = append(segments, tools.OneBotSegment{
					Type: "video",
					Data: map[string]any{"file": att.URL},
				})
			}
		default:
			logs.Warn(logs.WEBSOCKET, fmt.Sprintf("OneBot: 未知或不支持的附件类型 %q，已忽略", att.Type))
		}
	}

	// 将 TargetID 转为 int64 以符合 OneBot 规范（若非数字则保留原始字符串）
	var targetVal any = msg.TargetID
	if idInt, err := strconv.ParseInt(msg.TargetID, 10, 64); err == nil {
		targetVal = idInt
	}

	botAction := model.OneBotAction{
		Action: actionName,
		Params: map[string]any{
			idKey:     targetVal,
			"message": segments,
		},
		Echo: fmt.Sprintf("onebot_send_%s", msg.TargetID),
	}

	data, err := json.Marshal(botAction)
	if err != nil {
		return fmt.Errorf("onebot: 序列化 action 失败: %w", err)
	}

	var errs []error
	for _, c := range conns {
		if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
			logs.Error(logs.WEBSOCKET, fmt.Sprintf("OneBot Adapter Send 失败: %v", err))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ToIncomingMessage 将 model.OneBotEvent 转换为统一的 core.IncomingMessage。
// 供后续统一消息处理管道使用。
func ToIncomingMessage(event model.OneBotEvent) core.IncomingMessage {
	var senderName, senderCard string
	if event.Sender != nil {
		senderName = event.Sender.Nickname
		senderCard = event.Sender.Card
	}

	var groupID string
	if event.GroupID != 0 {
		groupID = strconv.FormatInt(event.GroupID, 10)
	}

	return core.IncomingMessage{
		ID:          strconv.FormatInt(int64(event.MessageID), 10),
		SessionID:   historyKey(event),
		UserID:      strconv.FormatInt(event.UserID, 10),
		SenderName:  senderName,
		SenderCard:  senderCard,
		Content:     string(event.Message),
		Platform:    "onebot",
		MessageType: event.MessageType,
		GroupID:     groupID,
		CreatedAt:   time.Now(),
		RawMessage:  event,
	}
}

// Handler 返回用于注册到 HTTP mux 的 WebSocket Handler
func (a *Adapter) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logs.Error(logs.WEBSOCKET, fmt.Sprintf("WebSocket 升级失败: %v", err))
			return
		}
		wsConn := newWSConnection(conn)
		wsConn.stealer = a.stealer
		a.registerConn(wsConn)
		defer func() {
			a.unregisterConn(wsConn)
			wsConn.Close()
		}()

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

			var routeSnapshot *modelrouter.Snapshot
			if a.engine != nil && a.engine.ModelRouter != nil && event.PostType == "message" &&
				(event.MessageType == "group" || event.MessageType == "private") {
				routeSnapshot = a.engine.ModelRouter.Snapshot()
				if routeSnapshot.IsDisabled(modelrouter.WorkloadDialogue, oneBotRouteScope(event)) {
					continue
				}
			}

			if event.PostType == "message" && event.MessageType == "group" {
				captureGroupCompactMessage(event, a.engine)
			}

			a.observeStickers(event)
			var turn *llm.SessionTurn
			if a.engine != nil && a.engine.SessionManager != nil && event.PostType == "message" &&
				(event.MessageType == "group" || event.MessageType == "private") {
				turn = a.engine.SessionManager.GetOrCreate(historyKey(event)).ReserveTurn()
			}
			go processEvent(wsConn, event, a.engine, turn, routeSnapshot)
		}
	}
}

func stickerSourcesFromSegments(segments []content.MessageSegment) []string {
	var sources []string
	for _, seg := range segments {
		if seg.Type != "mface" &&
			(seg.Type != "image" || (!isStickerSubType(seg.Data["sub_type"]) && !content.IsMarketFaceSegment(seg))) {
			continue
		}
		if source := content.SegmentImageSource(seg); source != "" {
			sources = append(sources, source)
		}
	}
	return sources
}

func observeStickerSources(
	stealer *sticker.Stealer,
	sessionID string,
	messageID string,
	sources []string,
	autoCollect bool,
) {
	for index, source := range sources {
		source := source
		stealer.Observe(
			sessionID,
			messageID,
			index,
			func(ctx context.Context) ([]byte, error) {
				return sticker.LoadImageSource(ctx, source)
			},
			autoCollect,
		)
	}
}

func isStickerSubType(value any) bool {
	switch stickerType := value.(type) {
	case int:
		return stickerType == 1
	case float64:
		return stickerType == 1
	case string:
		return stickerType == "1"
	case json.Number:
		return stickerType.String() == "1"
	default:
		return false
	}
}
