package onebot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

const maxQQInlineBytes int64 = 30 << 20

func nativeQQFinal(messages []tools.Msg) bool {
	if tools.NeedsQQDelivery(messages) {
		return true
	}
	for _, m := range messages {
		switch m.Type {
		case "image", "record", "video", "file":
			return true
		}
	}
	return false
}

func qqDeliveredText(messages []tools.Msg) string {
	var texts []string
	for _, m := range messages {
		switch m.Type {
		case "plain":
			texts = append(texts, m.Text)
		case "quote", "mention_specific":
		case "mention_user":
			texts = append(texts, "[@"+m.MentionUserID+"]")
		case "mention_all":
			texts = append(texts, "[@all]")
		default:
			label := m.Type
			if m.Data != nil {
				if m.Data.Title != "" {
					label += " " + m.Data.Title
				}
				if m.Data.ID != "" {
					label += " " + m.Data.ID
				}
				if m.Data.URL != "" {
					label += " " + m.Data.URL
				}
			}
			if m.Name != "" {
				label += " " + m.Name
			}
			texts = append(texts, "["+label+"]")
		}
	}
	return strings.TrimSpace(strings.Join(texts, " "))
}

type qqItemResult struct {
	Index     int    `json:"index"`
	Status    string `json:"status"`
	Action    string `json:"action,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	FileID    string `json:"file_id,omitempty"`
	Fallback  string `json:"fallback,omitempty"`
	Error     string `json:"error,omitempty"`
}

// qqSender is bound to one request's connection, conversation and turn authority.
// Tests replace call, while production always uses the ACK-correlated transport.
type qqSender struct {
	event     model.OneBotEvent
	call      func(model.OneBotAction) (oneBotAPIResponse, error)
	reference func(string) bool
	own       func(string) bool
	confirmed func(oneBotAPIResponse, []tools.Msg)
	decorate  func([]tools.OneBotSegment) []tools.OneBotSegment
}

func (c *wsConnection) qqActionHook(event model.OneBotEvent, turn *llm.SessionTurn, delivered func([]tools.Msg), engine *llm.Engine) func(context.Context, string) (core.PlatformActionResult, error) {
	return func(ctx context.Context, raw string) (core.PlatformActionResult, error) {
		var request tools.QQRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return core.PlatformActionResult{}, err
		}
		if err := tools.ValidateQQRequest(request); err != nil {
			return core.PlatformActionResult{}, err
		}
		if c.mock {
			return core.PlatformActionResult{}, fmt.Errorf("QQ 操作不能在模拟会话中发送")
		}
		sourceSessionID := c.historyKey(event)
		sessionID := sourceSessionID
		destination := event
		external := false
		if groupID := request.Arguments.TargetGroupID; groupID != "" {
			id, _ := qqextend.ParseGroupID(groupID) // ValidateQQRequest checked the ID.
			external = event.MessageType != "group" || event.GroupID != id
			if external {
				if !qqextend.TargetAllowed(qqextend.ConfiguredTargets(c.Scope), groupID) {
					return core.PlatformActionResult{}, fmt.Errorf("目标群不在 qq_extend_messages 跨会话白名单中")
				}
				for _, m := range request.Arguments.Messages {
					if m.Type == "quote" {
						return core.PlatformActionResult{}, fmt.Errorf("跨会话发送不能引用来源会话消息，请使用转发")
					}
				}
				destination.MessageType, destination.GroupID = "group", id
				destination.UserID, destination.MessageID, destination.Message = 0, 0, nil
				sessionID = c.historyKey(destination)
			}
		}
		var targetSession *llm.SessionContext
		var targetEpoch uint64
		if external {
			if engine == nil || engine.SessionManager == nil {
				return core.PlatformActionResult{}, fmt.Errorf("目标群会话不可用")
			}
			targetSession = engine.SessionManager.GetOrCreate(sessionID)
			targetEpoch = targetSession.Epoch()
		}
		resolve := func(messages []tools.Msg) error {
			for i, m := range messages {
				if m.Type == "mface" && m.MessageID != "" {
					resolved, err := c.resolveMarketFace(ctx, event, m)
					if err != nil {
						return err
					}
					messages[i] = resolved
				}
			}
			return nil
		}
		if err := resolve(request.Arguments.Messages); err != nil {
			return core.PlatformActionResult{}, err
		}
		for _, node := range request.Arguments.Nodes {
			if err := resolve(node.Messages); err != nil {
				return core.PlatformActionResult{}, err
			}
		}
		sender := qqSender{event: destination,
			call: func(action model.OneBotAction) (oneBotAPIResponse, error) {
				if err := ctx.Err(); err != nil {
					return oneBotAPIResponse{}, err
				}
				if turn.Interrupted() {
					return oneBotAPIResponse{}, context.Canceled
				}
				// Settings are hot: revocation must also stop later split parts and fallbacks.
				if external && !qqextend.TargetAllowed(qqextend.ConfiguredTargets(c.Scope), request.Arguments.TargetGroupID) {
					return oneBotAPIResponse{Status: "failed", RetCode: 403}, fmt.Errorf("目标群已移出跨会话白名单，未发送")
				}
				if external && targetSession.Epoch() != targetEpoch {
					return oneBotAPIResponse{Status: "failed", RetCode: 403}, fmt.Errorf("目标群会话已重置，未发送")
				}
				timeout := actionACKTimeout(c.Scope)
				if strings.HasPrefix(action.Action, "upload_") && timeout == defaultActionACKTimeout {
					timeout = 120 * time.Second
				}
				return c.sendActionAndWaitTurn(action, timeout, turn)
			},
			reference: func(id string) bool {
				mid, ok := numericMessageID(id)
				return ok && c.messageSessionMatches(mid, sourceSessionID)
			},
			own: func(id string) bool {
				mid, ok := numericMessageID(id)
				return ok && c.ownMessageSessionMatches(mid, sourceSessionID)
			},
			confirmed: func(response oneBotAPIResponse, messages []tools.Msg) {
				c.rememberActionMessageSession(response, sessionID)
				if external {
					recordQQTargetDelivery(engine, targetSession, targetEpoch, messages)
				} else if delivered != nil {
					delivered(messages)
				}
			},
			decorate: func(segments []tools.OneBotSegment) []tools.OneBotSegment {
				if !external && event.MessageType == "group" {
					decorationEvent := event
					if request.SuppressAutoMention {
						decorationEvent.UserID = 0
					}
					return wrapGroupReply(segments, decorationEvent, c.Scope)
				}
				return segments
			},
		}
		for _, m := range request.Arguments.Messages {
			if (m.IsSticker || m.Type == "mface") && sticker.ActivityLevel(c.Scope.Getenv(sticker.ActivityEnvKey)) == 0 {
				return core.PlatformActionResult{}, fmt.Errorf("表情包发送功能已被禁用")
			}
		}
		result, err := sender.execute(request)
		if external && err == nil {
			result.ExternalMessagesSent = result.MessagesSent
			var content map[string]any
			if json.Unmarshal([]byte(result.Content), &content) == nil {
				content["target_session"] = sessionID
				encoded, _ := json.Marshal(content)
				result.Content = string(encoded)
			}
		}
		return result, err
	}
}

func recordQQTargetDelivery(engine *llm.Engine, session *llm.SessionContext, epoch uint64, messages []tools.Msg) {
	text := qqDeliveredText(messages)
	if text == "" {
		return
	}
	session.WithEpochCommit(epoch, func() {
		session.AddMessageAtEpoch(core.ChatMessage{Role: core.RoleAssistant, Content: text}, epoch)
		name := engine.Getenv("BOT_NAME")
		if name == "" {
			name = defaultBotName
		}
		maxBuffer := 0
		if engine.GroupCompactor != nil {
			maxBuffer = engine.GroupCompactor.MaxBufferSize()
		}
		session.AppendGroupCompactMessage(llm.GroupCompactMessage{Role: "assistant", Sender: name, Content: text, Time: time.Now().Format("15:04:05")}, maxBuffer)
	})
	engine.TrimSession(session)
}

func (c *wsConnection) resolveMarketFace(ctx context.Context, event model.OneBotEvent, m tools.Msg) (tools.Msg, error) {
	mid, ok := numericMessageID(m.MessageID)
	if !ok || !c.messageSessionMatches(mid, c.historyKey(event)) {
		return tools.Msg{}, fmt.Errorf("商城表情来源不属于当前可信会话")
	}
	segments := ParseMessageSegments(event.Message)
	if mid != int64(event.MessageID) {
		segments = c.lookupMessageContext(ctx, event, mid).Segments
	}
	index := 0
	for _, segment := range segments {
		if !content.IsMarketFaceSegment(segment) {
			continue
		}
		if index != m.StickerIndex {
			index++
			continue
		}
		field := func(key string) string {
			if value := segment.Data[key]; value != nil {
				return fmt.Sprint(value)
			}
			return ""
		}
		resolved := tools.Msg{Type: "mface", Data: &tools.QQMessageData{EmojiID: field("emoji_id"), EmojiPackageID: field("emoji_package_id"), Key: field("key"), Summary: field("summary")}}
		if err := tools.ValidateQQMessages([]tools.Msg{resolved}, false); err != nil {
			return tools.Msg{}, fmt.Errorf("商城表情缺少可重发的原生元数据: %w", err)
		}
		return resolved, nil
	}
	return tools.Msg{}, fmt.Errorf("指定商城表情不存在")
}

func (s qqSender) target() map[string]any {
	if s.event.MessageType == "group" {
		return map[string]any{"group_id": s.event.GroupID}
	}
	return map[string]any{"user_id": s.event.UserID}
}

func (s qqSender) sendAction(segments []tools.OneBotSegment) model.OneBotAction {
	params := s.target()
	params["message"] = segments
	name := "send_private_msg"
	if s.event.MessageType == "group" {
		name = "send_group_msg"
	}
	return model.OneBotAction{Action: name, Params: params}
}

func (s qqSender) execute(request tools.QQRequest) (core.PlatformActionResult, error) {
	if err := tools.ValidateQQRequest(request); err != nil {
		return core.PlatformActionResult{}, err
	}
	if s.event.MessageType != "group" && s.event.MessageType != "private" {
		return core.PlatformActionResult{}, fmt.Errorf("QQ 操作需要群或私聊会话")
	}
	a := request.Arguments
	if request.Action == "send" {
		return s.send(a.Messages)
	}
	if a.MessageID != "" {
		if s.reference == nil || !s.reference(a.MessageID) {
			return core.PlatformActionResult{}, fmt.Errorf("message_id 不在当前连接、当前会话的可信上下文中")
		}
		if request.Action == "recall" && (s.own == nil || !s.own(a.MessageID)) {
			return core.PlatformActionResult{}, fmt.Errorf("只能撤回当前会话中已确认由机器人发送的消息")
		}
	}
	var candidates []model.OneBotAction
	var public []tools.Msg
	switch request.Action {
	case "poke":
		userID := s.event.UserID
		if a.UserID != "" {
			var err error
			userID, err = strconv.ParseInt(a.UserID, 10, 64)
			if err != nil || userID <= 0 {
				return core.PlatformActionResult{}, fmt.Errorf("user_id 必须为有效数字 ID")
			}
		}
		if s.event.MessageType == "private" && userID != s.event.UserID {
			return core.PlatformActionResult{}, fmt.Errorf("私聊只能戳当前对方")
		}
		params := map[string]any{"user_id": userID}
		name := "friend_poke"
		if s.event.MessageType == "group" {
			params["group_id"] = s.event.GroupID
			name = "group_poke"
		}
		candidates = []model.OneBotAction{{Action: "send_poke", Params: params}, {Action: name, Params: params}}
	case "reaction":
		if s.event.MessageType != "group" {
			return core.PlatformActionResult{}, fmt.Errorf("表情回应需要群聊")
		}
		set := true
		if a.Set != nil {
			set = *a.Set
		}
		candidates = []model.OneBotAction{
			{Action: "set_msg_emoji_like", Params: map[string]any{"message_id": a.MessageID, "emoji_id": a.EmojiID, "set": set}},
			{Action: "set_group_reaction", Params: map[string]any{"group_id": s.event.GroupID, "message_id": a.MessageID, "code": a.EmojiID, "is_set": set}},
		}
	case "recall":
		candidates = []model.OneBotAction{{Action: "delete_msg", Params: map[string]any{"message_id": a.MessageID}}}
	case "forward_message":
		params := s.target()
		params["message_id"] = a.MessageID
		name := "forward_friend_single_msg"
		if s.event.MessageType == "group" {
			name = "forward_group_single_msg"
		}
		candidates = []model.OneBotAction{{Action: name, Params: params}}
		forward := s.forwardActions([]tools.OneBotSegment{{Type: "node", Data: map[string]any{"id": a.MessageID}}}, a)
		candidates = append(candidates, forward...)
		public = []tools.Msg{{Type: "plain", Text: "[转发消息 " + a.MessageID + "]"}}
	case "forward_bundle":
		nodes := make([]tools.OneBotSegment, 0, len(a.Nodes))
		for _, n := range a.Nodes {
			if n.MessageID != "" {
				if s.reference == nil || !s.reference(n.MessageID) {
					return core.PlatformActionResult{}, fmt.Errorf("转发节点引用不属于当前可信会话")
				}
				nodes = append(nodes, tools.OneBotSegment{Type: "node", Data: map[string]any{"id": n.MessageID}})
				continue
			}
			segments, err := tools.BuildOneBotMessage(n.Messages)
			if err != nil {
				return core.PlatformActionResult{}, err
			}
			normalizeQQCards(n.Messages, segments)
			nodes = append(nodes, tools.OneBotSegment{Type: "node", Data: map[string]any{"user_id": strconv.FormatInt(s.event.SelfID, 10), "nickname": "FrostAgent", "content": segments}})
		}
		candidates = s.forwardActions(nodes, a)
		public = []tools.Msg{{Type: "plain", Text: fmt.Sprintf("[合并转发 %d 条消息]", len(nodes))}}
	}
	response, action, fallback, err := s.tryActions(candidates)
	item := qqResult(0, response, action, fallback, err)
	result := core.PlatformActionResult{}
	if err == nil && len(public) > 0 {
		result.MessagesSent = 1
		if s.confirmed != nil {
			s.confirmed(response, public)
		}
	}
	result.Content = encodeQQResults([]qqItemResult{item})
	return result, nil
}

func (s qqSender) forwardActions(nodes []tools.OneBotSegment, a tools.QQArguments) []model.OneBotAction {
	params := s.target()
	params["messages"] = nodes
	if a.Source != "" {
		params["source"] = a.Source
	}
	if a.Summary != "" {
		params["summary"] = a.Summary
	}
	if a.Prompt != "" {
		params["prompt"] = a.Prompt
	}
	if len(a.News) > 0 {
		news := make([]map[string]string, 0, len(a.News))
		for _, text := range a.News {
			news = append(news, map[string]string{"text": text})
		}
		params["news"] = news
	}
	name := "send_private_forward_msg"
	if s.event.MessageType == "group" {
		name = "send_group_forward_msg"
	}
	return []model.OneBotAction{{Action: name, Params: params}, {Action: "send_forward_msg", Params: params}}
}

func (s qqSender) tryActions(actions []model.OneBotAction) (oneBotAPIResponse, string, string, error) {
	var response oneBotAPIResponse
	var err error
	var name, fallback string
	for i, action := range actions {
		name = action.Action
		response, err = s.call(action)
		if i > 0 {
			fallback = qqActionDescription(action)
		}
		if err == nil || !qqUnsupported(response) {
			return response, name, fallback, err
		}
	}
	return response, name, fallback, err
}

func qqActionDescription(action model.OneBotAction) string {
	if params, ok := action.Params.(map[string]any); ok {
		if messages, ok := params["message"].([]tools.OneBotSegment); ok && len(messages) == 1 {
			return action.Action + "/" + messages[0].Type
		}
	}
	return action.Action
}

// Only an explicit, terminal rejection may select a compatibility action.
// Async/timeout/disconnect responses do not prove that a write was unexecuted.
func qqUnsupported(response oneBotAPIResponse) bool {
	if response.Status != "failed" || response.RetCode == 0 {
		return false
	}
	if response.RetCode == 1404 {
		return true
	}
	text := strings.ToLower(response.Message + " " + response.Wording)
	for _, pattern := range []string{"unknown action", "unknown api", "unsupported action", "unsupported api", "action not found", "api not found", "not implemented", "unsupported message type", "unsupported segment", "不支持的消息类型", "不支持该消息类型", "不支持的消息段", "接口不存在", "未实现的接口"} {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func qqPathRejected(response oneBotAPIResponse) bool {
	if response.Status != "failed" || response.RetCode == 0 {
		return false
	}
	text := strings.ToLower(response.Message + " " + response.Wording)
	for _, pattern := range []string{"no such file", "file not found", "enoent", "cannot access file", "cannot read file", "文件不存在", "找不到文件", "无法读取文件", "无法访问文件"} {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func qqResult(index int, response oneBotAPIResponse, action, fallback string, err error) qqItemResult {
	item := qqItemResult{Index: index, Status: "confirmed", Action: action, Fallback: fallback}
	if err != nil {
		item.Status = "unknown"
		if response.Status == "failed" && response.RetCode != 0 {
			item.Status = "rejected"
		}
		item.Error = err.Error()
		return item
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(response.Data, &data) == nil {
		item.MessageID = qqResponseID(data["message_id"])
		item.FileID = qqResponseID(data["file_id"])
	}
	return item
}

func qqResponseID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func encodeQQResults(items []qqItemResult) string {
	ok := true
	for _, item := range items {
		if item.Status != "confirmed" {
			ok = false
		}
	}
	b, _ := json.Marshal(map[string]any{"ok": ok, "results": items, "retry_unknown": false})
	return string(b)
}

type qqSendPart struct {
	start, end int
	messages   []tools.Msg
	segments   []tools.OneBotSegment
	format     string
}

func qqSeparateMessage(m tools.Msg) bool {
	switch m.Type {
	case "file", "share", "location", "mface":
		return true
	}
	return false
}

func (s qqSender) prepare(messages []tools.Msg) ([]qqSendPart, error) {
	if err := tools.ValidateQQMessages(messages, false); err != nil {
		return nil, err
	}
	// Validate the entire sequence before the first write.
	for _, m := range messages {
		if m.Type == "mention_all" && s.event.MessageType != "group" {
			return nil, fmt.Errorf("mention_all 需要群聊")
		}
		if m.FolderID != "" && s.event.MessageType != "group" {
			return nil, fmt.Errorf("folder_id 需要群聊文件")
		}
		if m.Type == "quote" && (s.reference == nil || !s.reference(m.MessageID)) {
			return nil, fmt.Errorf("引用消息不在当前可信会话")
		}
	}
	parts := []qqSendPart{}
	inlineBudget := maxQQInlineBytes
	for start := 0; start < len(messages); {
		end := start + 1
		if !qqSeparateMessage(messages[start]) {
			for end < len(messages) && !qqSeparateMessage(messages[end]) {
				end++
			}
			prefixOnly := true
			for _, m := range messages[start:end] {
				if m.Type != "quote" && m.Type != "mention_user" && m.Type != "mention_all" {
					prefixOnly = false
				}
			}
			if prefixOnly && end < len(messages) {
				if messages[end].Type == "file" && messages[end].FolderID != "" {
					return nil, fmt.Errorf("带引用或提及前缀的文件不能同时选择上传目录")
				}
				end++
			}
			if prefixOnly && end == len(messages) && messages[end-1].Type == "quote" {
				return nil, fmt.Errorf("引用消息必须附带正文")
			}
		}
		part := qqSendPart{start: start, end: end, messages: append([]tools.Msg(nil), messages[start:end]...)}
		wireMessages := append([]tools.Msg(nil), part.messages...)
		for i, m := range wireMessages {
			if m.Path != "" && (m.Transfer == "inline" || m.Type == "image" && m.IsSticker) {
				source, err := qqInlineFileLimit(m.Path, inlineBudget)
				if err != nil {
					return nil, err
				}
				inlineBudget -= int64(len(source)-len("base64://")) * 3 / 4
				m.Path = ""
				m.URL = source
			}
			if m.Thumb != "" && !strings.Contains(m.Thumb, "://") && m.Transfer == "inline" {
				source, err := qqInlineFileLimit(m.Thumb, inlineBudget)
				if err != nil {
					return nil, err
				}
				inlineBudget -= int64(len(source)-len("base64://")) * 3 / 4
				m.Thumb = source
			}
			wireMessages[i] = m
		}
		segments, err := tools.BuildOneBotMessage(wireMessages)
		if err != nil {
			return nil, err
		}
		part.format = normalizeQQCards(wireMessages, segments)
		part.segments = segments
		parts = append(parts, part)
		start = end
	}
	return parts, nil
}

func (s qqSender) send(messages []tools.Msg) (core.PlatformActionResult, error) {
	parts, err := s.prepare(messages)
	if err != nil {
		return core.PlatformActionResult{}, err
	}
	items := make([]qqItemResult, len(messages))
	for i := range items {
		items[i] = qqItemResult{Index: i, Status: "not_sent"}
	}
	result := core.PlatformActionResult{}
	for _, part := range parts {
		response, action, fallback, err := s.sendPart(part)
		for i := part.start; i < part.end; i++ {
			items[i] = qqResult(i, response, action, fallback, err)
		}
		if err != nil {
			break
		}
		// Decorate only the first confirmed message in this logical send.
		// Standalone uploads cannot carry a reply/mention prefix.
		if action == "send_group_msg" || action == "send_private_msg" {
			s.decorate = nil
		}
		result.MessagesSent++
		for _, m := range part.messages {
			result.StickerSent = result.StickerSent || m.IsSticker || m.Type == "mface"
		}
		if s.confirmed != nil {
			s.confirmed(response, part.messages)
		}
	}
	result.Content = encodeQQResults(items)
	return result, nil
}

func (s qqSender) sendPart(part qqSendPart) (oneBotAPIResponse, string, string, error) {
	segments := part.segments
	if s.decorate != nil {
		segments = s.decorate(segments)
	}
	action := s.sendAction(segments)
	var response oneBotAPIResponse
	var err error
	fallback := part.format
	isFile := len(part.messages) == 1 && part.messages[0].Type == "file"
	if isFile {
		action = s.uploadFileAction(part.messages[0], part.segments[0].Data["file"].(string))
	}
	response, err = s.call(action)
	if err == nil {
		return response, action.Action, fallback, nil
	}
	if qqPathRejected(response) {
		chain, inlineErr := qqInlinePart(part)
		if inlineErr != nil {
			return response, action.Action, "", fmt.Errorf("%v; inline fallback: %w", err, inlineErr)
		}
		if chain != nil {
			segments = chain
			if s.decorate != nil {
				segments = s.decorate(segments)
			}
			if isFile {
				action = s.uploadFileAction(part.messages[0], chain[0].Data["file"].(string))
			} else {
				action = s.sendAction(segments)
			}
			response, err = s.call(action)
			fallback = "inline"
		}
	}
	if len(part.messages) == 1 || qqSeparateMessage(part.messages[len(part.messages)-1]) {
		m := part.messages[len(part.messages)-1]
		// Uploading a quoted file would discard its explicit reply prefix.
		if m.Type == "file" && len(part.messages) > 1 {
			return response, action.Action, fallback, err
		}
		if err != nil && qqUnsupported(response) {
			compat := s.componentFallback(m, segments)
			if len(compat) > 0 {
				response, name, used, e := s.tryActions(compat)
				if used == "" {
					used = qqActionDescription(compat[0])
				}
				return response, name, used, e
			}
		}
	}
	return response, action.Action, fallback, err
}

func qqInlinePart(part qqSendPart) ([]tools.OneBotSegment, error) {
	budget := maxQQInlineBytes
	// Account for media already inlined during preparation before admitting any
	// additional fallback reads from this same message (including thumbnails).
	for _, segment := range part.segments {
		for _, key := range []string{"file", "thumb"} {
			source, _ := segment.Data[key].(string)
			if strings.HasPrefix(source, "base64://") {
				budget -= qqInlineSize(source)
			}
		}
	}
	if budget < 0 {
		return nil, fmt.Errorf("inline media exceeds the shared 30 MiB budget")
	}
	chain := make([]tools.OneBotSegment, len(part.segments))
	changed := false
	for i, segment := range part.segments {
		data := map[string]any{}
		for key, value := range segment.Data {
			data[key] = value
		}
		for _, key := range []string{"file", "thumb"} {
			source, _ := data[key].(string)
			if !strings.HasPrefix(source, "file://") {
				continue
			}
			mode := part.messages[i].Transfer
			if mode == "shared" {
				return nil, nil
			}
			inline, err := qqInlineFileLimit(strings.TrimPrefix(source, "file://"), budget)
			if err != nil {
				return nil, err
			}
			budget -= int64(len(inline)-len("base64://")) * 3 / 4
			data[key] = inline
			changed = true
		}
		chain[i] = tools.OneBotSegment{Type: segment.Type, Data: data}
	}
	if !changed {
		return nil, nil
	}
	return chain, nil
}

func qqInlineSize(source string) int64 {
	encoded := strings.TrimPrefix(source, "base64://")
	size := base64.StdEncoding.DecodedLen(len(encoded))
	if strings.HasSuffix(encoded, "==") {
		size -= 2
	} else if strings.HasSuffix(encoded, "=") {
		size--
	}
	return int64(size)
}

func (s qqSender) uploadFileAction(m tools.Msg, source string) model.OneBotAction {
	params := s.target()
	params["file"] = strings.TrimPrefix(source, "file://")
	name := m.Name
	if name == "" && m.Path != "" {
		name = filepath.Base(m.Path)
	}
	if name == "" {
		u, _ := url.Parse(m.URL)
		name = filepath.Base(u.Path)
		if name == "." || name == "/" || name == "" {
			name = "attachment"
		}
	}
	params["name"] = name
	action := "upload_private_file"
	if s.event.MessageType == "group" {
		action = "upload_group_file"
		if m.FolderID != "" {
			params["folder"] = m.FolderID
			params["folder_id"] = m.FolderID
		}
	}
	return model.OneBotAction{Action: action, Params: params}
}

func (s qqSender) componentFallback(m tools.Msg, segments []tools.OneBotSegment) []model.OneBotAction {
	prefix := segments[:len(segments)-1]
	messageAction := func(segment tools.OneBotSegment) model.OneBotAction {
		chain := append(append([]tools.OneBotSegment(nil), prefix...), segment)
		return s.sendAction(chain)
	}
	switch m.Type {
	case "file":
		// A file segment cannot preserve a selected destination folder.
		if m.FolderID == "" {
			return []model.OneBotAction{s.sendAction(segments)}
		}
	case "mface":
		b, _ := json.Marshal(m.Data)
		data := map[string]any{}
		_ = json.Unmarshal(b, &data)
		data["file"] = "marketface"
		data["sub_type"] = 1
		data["subType"] = 1
		return []model.OneBotAction{messageAction(tools.OneBotSegment{Type: "image", Data: data})}
	case "share":
		fallthrough
	case "location":
		d := qqCardData(m)
		params := s.target()
		params["title"] = d.Title
		params["desc"] = d.Content
		params["jump_url"] = d.URL
		params["preview_url"] = d.Image
		var candidates []model.OneBotAction
		if len(prefix) == 0 {
			candidates = append(candidates, model.OneBotAction{Action: "send_tuwen_ark", Params: params})
		}
		return append(candidates, messageAction(tools.OneBotSegment{Type: "text", Data: map[string]any{"text": d.Title + "\n" + d.Content + "\n" + d.URL}}))
	}
	return nil
}

func qqCardData(m tools.Msg) *tools.QQMessageData {
	if m.Type == "share" {
		return m.Data
	}
	d := *m.Data
	d.URL = fmt.Sprintf("https://apis.map.qq.com/uri/v1/marker?marker=coord:%g,%g;title:%s;addr:%s&referer=FrostAgent", *d.Lat, *d.Lon, url.QueryEscape(d.Title), url.QueryEscape(d.Content))
	if d.Title == "" {
		d.Title = "位置"
	}
	d.Content = strings.TrimSpace(fmt.Sprintf("%s (%g, %g)", d.Content, *d.Lat, *d.Lon))
	return &d
}

func qqShareSegment(d *tools.QQMessageData) tools.OneBotSegment {
	ark, _ := json.Marshal(map[string]any{"app": "com.tencent.structmsg", "view": "news", "ver": "0.0.0.1", "prompt": "[分享] " + d.Title, "meta": map[string]any{"news": map[string]any{"title": d.Title, "desc": d.Content, "jumpUrl": d.URL, "preview": d.Image, "tag": "链接分享"}}})
	return tools.OneBotSegment{Type: "json", Data: map[string]any{"data": string(ark)}}
}

// Both direct messages and custom forward nodes use the same wire-safe card
// representation. Unsupported raw share/location segments may be discarded.
func normalizeQQCards(messages []tools.Msg, segments []tools.OneBotSegment) string {
	format := ""
	for i, m := range messages {
		if m.Type == "share" || m.Type == "location" {
			segments[i] = qqShareSegment(qqCardData(m))
			format = m.Type + "_ark"
		}
	}
	return format
}

// Inline transfer is bounded both before and during the read (the file may grow).
func qqInlineFile(path string) (string, error) { return qqInlineFileLimit(path, maxQQInlineBytes) }

func qqInlineFileLimit(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return "", fmt.Errorf("inline media must be a regular file of 1..30 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return "", fmt.Errorf("inline media exceeds 30 MiB or is empty")
	}
	return "base64://" + base64.StdEncoding.EncodeToString(data), nil
}
