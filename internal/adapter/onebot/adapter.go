package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/security"
	"FrostAgent/internal/sticker"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

func (c *wsConnection) observeStickers(event model.OneBotEvent) {
	if c == nil || c.stealer == nil || event.PostType != "message" ||
		(event.MessageType != "group" && event.MessageType != "private") {
		return
	}
	messageID := strconv.FormatInt(int64(event.MessageID), 10)
	observeStickerSources(
		c.stealer,
		c.generation,
		historyKey(event),
		messageID,
		stickerSourcesFromSegments(ParseMessageSegments(event.Message)),
		event.MessageType == "group",
	)
}

func (a *Adapter) observeStickers(event model.OneBotEvent) {
	if a.stealer == nil || event.PostType != "message" ||
		(event.MessageType != "group" && event.MessageType != "private") {
		return
	}
	a.mu.RLock()
	var firstConn *wsConnection
	for c := range a.conns {
		firstConn = c
		break
	}
	a.mu.RUnlock()
	scope := ""
	if firstConn != nil {
		scope = firstConn.generation
	}
	messageID := strconv.FormatInt(int64(event.MessageID), 10)
	observeStickerSources(
		a.stealer,
		scope,
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
		if err := validateOutboundMediaURL(att.URL); err != nil {
			return fmt.Errorf("onebot: %w", err)
		}
		switch att.Type {
		case core.AttachmentTypeImage:
			data := map[string]any{"file": att.URL}
			if att.SubType == 1 {
				data["sub_type"] = 1
				data["subType"] = 1
			}
			segments = append(segments, tools.OneBotSegment{
				Type: "image",
				Data: data,
			})
		case core.AttachmentTypeAudio:
			segments = append(segments, tools.OneBotSegment{
				Type: "record",
				Data: map[string]any{"file": att.URL},
			})
		case core.AttachmentTypeVideo:
			segments = append(segments, tools.OneBotSegment{
				Type: "video",
				Data: map[string]any{"file": att.URL},
			})
		default:
			return fmt.Errorf("onebot: unsupported attachment type %q", att.Type)
		}
	}

	if len(segments) == 0 {
		return fmt.Errorf("onebot: cannot send empty message (no content and no valid attachments)")
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
			if a.engine != nil {
				a.engine.Log().Error(logs.WEBSOCKET, fmt.Sprintf("OneBot Adapter Send 失败: %v", err))
			}
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
		if a.engine != nil {
			done, ok := a.engine.Enter()
			if !ok {
				http.Error(w, "实例未启用", http.StatusServiceUnavailable)
				return
			}
			defer done()
		}
		localUpgrader := upgrader
		if a.engine != nil && a.engine.Scope != nil {
			localUpgrader.CheckOrigin = a.engine.CheckOrigin
		}

		conn, err := localUpgrader.Upgrade(w, r, nil)
		if err != nil {
			if a.engine != nil {
				a.engine.Log().Error(logs.WEBSOCKET, fmt.Sprintf("WebSocket 升级失败: %v", err))
			}
			return
		}
		var wsConn *wsConnection
		if a.engine != nil {
			wsConn = newWSConnection(conn, a.engine.Scope)
			wsConn.Scope = a.engine.Scope
		} else {
			wsConn = newWSConnection(conn)
		}
		wsConn.stealer = a.stealer
		if r.URL.Query().Get("mock") == "true" || r.Header.Get("X-Mock-Adapter") == "true" {
			wsConn.mock = true
		}
		a.registerConn(wsConn)
		if a.engine != nil && a.engine.Context().Err() != nil {
			wsConn.Close()
		}
		defer func() {
			a.unregisterConn(wsConn)
			wsConn.Close()
			wsConn.inFlight.Wait()
			if wsConn.stealer != nil {
				wsConn.stealer.ClearObservedScope(wsConn.generation)
			}
			if wsConn.mock && a.engine != nil {
				if a.engine.SessionManager != nil {
					wsConn.mockSessions.Range(func(key, _ any) bool {
						if sessionID, ok := key.(string); ok {
							a.engine.SessionManager.Delete(sessionID)
						}
						return true
					})
				}
				if a.engine.SandboxBackend != nil {
					wsConn.mockSessions.Range(func(key, _ any) bool {
						if sessionID, ok := key.(string); ok {
							if err := a.engine.SandboxBackend.Release(context.Background(), sessionID); err != nil && !errors.Is(err, sandbox.ErrSandboxDisabled) {
								a.engine.Log().Warn(logs.SYSTEM, fmt.Sprintf("释放 mock sandbox session %s 失败: %v", sessionID, err))
							}
						}
						return true
					})
				}
			}
		}()

		if a.engine != nil {
			a.engine.Log().Info(logs.WEBSOCKET, fmt.Sprintf("WebSocket 连接已建立: %s", r.RemoteAddr))
		}

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if a.engine != nil {
					a.engine.Log().Error(logs.WEBSOCKET, fmt.Sprintf("读取消息失败: %v", err))
				}
				break
			}

			if wsConn.handleAPIResponse(message) {
				continue
			}

			var event model.OneBotEvent
			if err := json.Unmarshal(message, &event); err != nil {
				if a.engine != nil {
					a.engine.Log().Error(logs.WEBSOCKET, fmt.Sprintf("消息解析失败: %v", err))
				}
				continue
			}

			if event.MetaEventType == "heartbeat" {
				continue
			}
			if event.PostType == "message" &&
				(event.MessageType == "group" || event.MessageType == "private") {
				wsConn.rememberMessageSession(int64(event.MessageID), wsConn.historyKey(event))
				if !wsConn.mock && handleAdminCommand(wsConn, event, a.engine) {
					continue
				}
			}
			if a.engine != nil && a.engine.Security != nil && event.PostType == "message" &&
				(event.MessageType == "group" || event.MessageType == "private") {
				secPlatform := "onebot"
				if wsConn.mock {
					secPlatform = "mock"
				}
				principal, principalErr := security.NewPrincipal(secPlatform, strconv.FormatInt(event.UserID, 10))
				if principalErr != nil {
					continue
				}
				var decision security.WatchdogDecision
				if wsConn.mock {
					decision = a.engine.Security.GateIngressDryRun(principal, string(event.Message), security.AuditEvent{Instance: a.engine.InstanceID, Session: wsConn.historyKey(event)})
				} else {
					decision = a.engine.Security.GateIngress(principal, string(event.Message), security.AuditEvent{Instance: a.engine.InstanceID, Session: wsConn.historyKey(event)})
				}
				if security.Blocks(decision.Action) {
					logs.Warn(logs.SYSTEM, fmt.Sprintf("OneBot 消息被安全控制拦截: user=%d action=%s reason=%s", event.UserID, decision.Action, decision.Reason))
					if shouldSendSecurityDirectReply(event, a.engine) {
						msg := a.engine.Security.RejectMessage(principal, decision)
						action := "send_private_msg"
						type1 := "user_id"
						id := strconv.FormatInt(event.UserID, 10)
						if event.MessageType == "group" {
							action = "send_group_msg"
							type1 = "group_id"
							id = strconv.FormatInt(event.GroupID, 10)
						}
						sendDirectReply(action, type1, id, "echo_security_gate", event, wsConn, msg)
					}
					continue
				}
			}

			var routeSnapshot *modelrouter.Snapshot
			if a.engine != nil && a.engine.ModelRouter != nil && event.PostType == "message" &&
				(event.MessageType == "group" || event.MessageType == "private") {
				routeSnapshot = a.engine.ModelRouter.Snapshot()
				if routeSnapshot.IsDisabled(modelrouter.WorkloadDialogue, oneBotRouteScope(event)) {
					continue
				}
			}

			if event.PostType == "message" && event.MessageType == "group" && !wsConn.mock {
				captureGroupCompactMessage(event, a.engine)
			}

			if !wsConn.mock {
				wsConn.observeStickers(event)
			}
			if wsConn.isClosed() {
				continue
			}
			var turn *llm.SessionTurn
			if a.engine != nil && a.engine.SessionManager != nil && event.PostType == "message" &&
				(event.MessageType == "group" || event.MessageType == "private") {
				turn = a.engine.SessionManager.GetOrCreate(wsConn.historyKey(event)).ReserveTurn()
			}
			wsConn.inFlight.Add(1)
			if !a.engine.Go(func() {
				defer wsConn.inFlight.Done()
				processEvent(wsConn, event, a.engine, turn, routeSnapshot)
			}) {
				wsConn.inFlight.Done()
				if turn != nil {
					turn.Done()
				}
			}
		}
	}
}

func stickerSourcesFromSegments(segments []content.MessageSegment) []string {
	var sources []string
	for _, seg := range segments {
		if seg.Type != "mface" &&
			(seg.Type != "image" || (!isStickerSubType(content.ImageSubType(seg.Data)) && !content.IsMarketFaceSegment(seg))) {
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
	scope string,
	sessionID string,
	messageID string,
	sources []string,
	autoCollect bool,
) {
	for index, source := range sources {
		var loader sticker.ImageLoader
		if autoCollect {
			source := source
			loader = func(ctx context.Context) ([]byte, error) {
				return sticker.LoadImageSource(ctx, source)
			}
		}
		stealer.ObserveScoped(
			sessionID,
			messageID,
			index,
			scope,
			loader,
			autoCollect,
		)
	}
}

func isStickerSubType(value any) bool {
	switch stickerType := value.(type) {
	case int:
		return stickerType == 1
	case int64:
		return stickerType == 1
	case float64:
		return stickerType == 1
	case string:
		return strings.TrimSpace(stickerType) == "1"
	case json.Number:
		return stickerType.String() == "1"
	default:
		return false
	}
}

func (a *Adapter) CloseConnections() {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for c := range a.conns {
		c.Close()
	}
}

func shouldSendSecurityDirectReply(event model.OneBotEvent, engine *llm.Engine) bool {
	if event.MessageType == "private" {
		return true
	}
	if event.MessageType != "group" {
		return false
	}
	if engine != nil && engine.Getenv("GROUP_REPLY_ON_MENTION") == "false" {
		return true
	}
	if engine != nil && DetectGroupWakeSignals(event, engine.Scope).Any() {
		return true
	}
	return false
}

func validateOutboundMediaURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errors.New("attachment requires a valid 'url': local 'path' without url is unsupported")
	}

	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "file:") || strings.HasPrefix(lower, "file/") {
		return fmt.Errorf("insecure media url %q: file:// scheme is forbidden", rawURL)
	}
	if len(rawURL) >= 2 && rawURL[1] == ':' && ((rawURL[0] >= 'a' && rawURL[0] <= 'z') || (rawURL[0] >= 'A' && rawURL[0] <= 'Z')) {
		return fmt.Errorf("insecure media url %q: local drive path is forbidden", rawURL)
	}
	if strings.HasPrefix(rawURL, `\\`) || strings.HasPrefix(rawURL, "//") {
		return fmt.Errorf("insecure media url %q: UNC or network path without scheme is forbidden", rawURL)
	}
	if strings.HasPrefix(rawURL, "/") || strings.HasPrefix(rawURL, ".") {
		return fmt.Errorf("insecure media url %q: local path without scheme is forbidden", rawURL)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid media url %q: %w", rawURL, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https":
		if parsed.Host == "" {
			return fmt.Errorf("insecure media url %q: missing host", rawURL)
		}
		return nil
	case "base64":
		return nil
	default:
		return fmt.Errorf("unsupported media url scheme %q: only http, https, and base64 are allowed", scheme)
	}
}

