package astrbot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/quotetrace"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"strings"
	"sync/atomic"
	"time"
)

const quoteLookupTimeout = 6 * time.Second
const quoteChainTimeout = 10 * time.Second

var nextQuoteLookupID uint64

type quoteLookupResponse struct {
	Type string          `json:"type"`
	Echo string          `json:"echo"`
	Data json.RawMessage `json:"data"`
}

type astrBotQuoteNode struct {
	Context         map[string]any
	Segments        []content.MessageSegment
	ParentID        string
	ForwardedImages []content.MessageSegment
}

func (c *wsConn) acceptQuoteLookup(response quoteLookupResponse) {
	if pending, ok := c.pendingQuotes.LoadAndDelete(response.Echo); ok {
		pending.(chan quoteLookupResponse) <- response
	}
}

func (c *wsConn) lookupQuotedMessage(ctx context.Context, event Event, messageID string) json.RawMessage {
	if c == nil || c.conn == nil || c.mock {
		return nil
	}
	echo := fmt.Sprintf("quote_lookup_%d", atomic.AddUint64(&nextQuoteLookupID, 1))
	pending := make(chan quoteLookupResponse, 1)
	c.pendingQuotes.Store(echo, pending)
	defer c.pendingQuotes.Delete(echo)
	if err := c.WriteJSON(map[string]string{
		"type": "quote_lookup", "echo": echo,
		"event_message_id": event.MessageID, "message_id": messageID,
	}); err != nil {
		return nil
	}
	timer := time.NewTimer(quoteLookupTimeout)
	defer timer.Stop()
	select {
	case response := <-pending:
		return response.Data
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return nil
	}
}

func quoteLookupSupported(event Event) bool {
	return event.Metadata != nil && event.Metadata["quote_lookup_supported"] == true
}

func directQuotedImageChain(event Event) []astrBotQuoteNode {
	if event.Metadata == nil {
		return nil
	}
	replyID, _ := event.Metadata["reply_message_id"].(string)
	replyID = strings.TrimSpace(replyID)
	if replyID == "" {
		return nil
	}
	var segments []content.MessageSegment
	for _, att := range event.Attachments {
		if att.MessageID != replyID || att.Type != core.AttachmentTypeImage || (len(att.Content) == 0 && att.URL == "") {
			continue
		}
		data := make(map[string]any)
		if len(att.Content) > 0 {
			data["base64"] = base64.StdEncoding.EncodeToString(att.Content)
		} else {
			data["url"] = att.URL
		}
		segments = append(segments, content.MessageSegment{Type: "image", Data: data})
	}
	if len(segments) == 0 {
		return nil
	}
	contextData := map[string]any{"message_id": replyID, "message": "[图片]"}
	if event.MessageType == "group" {
		contextData["group_id"] = event.GroupID
	}
	return []astrBotQuoteNode{{Context: contextData, Segments: segments}}
}

func (c *wsConn) lookupQuoteChain(event Event) ([]astrBotQuoteNode, bool) {
	depth := quotetrace.Depth(c.Scope.Getenv)
	if depth == 0 || event.Platform != astrBotQQPlatform || !quoteLookupSupported(event) {
		return nil, true
	}
	messageID, _ := event.Metadata["reply_message_id"].(string)
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(c.Context(), quoteChainTimeout)
	defer cancel()
	seen := map[string]struct{}{event.MessageID: {}}
	chain := make([]astrBotQuoteNode, 0, depth)
	for len(chain) < depth && messageID != "" {
		if _, exists := seen[messageID]; exists {
			return chain, false
		}
		seen[messageID] = struct{}{}
		data := c.lookupQuotedMessage(ctx, event, messageID)
		node, ok := parseAstrBotQuote(event, messageID, data)
		if !ok {
			if len(data) > 0 && strings.TrimSpace(string(data)) != "{}" {
				return chain, false
			}
			break
		}
		chain = append(chain, node)
		messageID = node.ParentID
	}
	return chain, true
}

func quoteField(data map[string]json.RawMessage, key string) string {
	raw := data[key]
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		if _, err := number.Int64(); err == nil {
			return number.String()
		}
	}
	return ""
}

func quotedParentID(raw json.RawMessage) string {
	var entries []struct {
		Type string
		Data map[string]json.RawMessage
	}
	if json.Unmarshal(raw, &entries) == nil {
		for _, entry := range entries {
			if entry.Type == "reply" {
				return quoteField(entry.Data, "id")
			}
		}
		return ""
	}
	for _, segment := range onebot.ParseMessageSegments(raw) {
		if segment.Type == "reply" {
			return strings.TrimSpace(fmt.Sprint(segment.Data["id"]))
		}
	}
	return ""
}

func parseAstrBotQuote(event Event, requestedID string, raw json.RawMessage) (astrBotQuoteNode, bool) {
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil || len(data) == 0 {
		return astrBotQuoteNode{}, false
	}
	id := quoteField(data, "message_id")
	if id != "" && id != requestedID {
		return astrBotQuoteNode{}, false
	}
	if id == "" {
		id = requestedID
	}
	messageType := quoteField(data, "message_type")
	switch event.MessageType {
	case "group":
		if messageType != "" && messageType != "group" {
			return astrBotQuoteNode{}, false
		}
		if quoteField(data, "group_id") == "" || quoteField(data, "group_id") != event.GroupID {
			return astrBotQuoteNode{}, false
		}
	case "private":
		if quoteField(data, "group_id") != "" || (messageType != "" && messageType != "private") {
			return astrBotQuoteNode{}, false
		}
		if quoteField(data, "user_id") != event.UserID && quoteField(data, "target_id") != event.UserID && quoteField(data, "peer_id") != event.UserID {
			return astrBotQuoteNode{}, false
		}
	default:
		return astrBotQuoteNode{}, false
	}
	segments := onebot.ParseMessageSegments(data["message"])
	var visible strings.Builder
	parentID := quotedParentID(data["message"])
	for _, segment := range segments {
		switch segment.Type {
		case "text":
			visible.WriteString(fmt.Sprint(segment.Data["text"]))
		case "image":
			visible.WriteString("[图片]")
		}
	}
	contextData := map[string]any{"message_id": id, "message": strings.TrimSpace(visible.String())}
	if senderID := quoteField(data, "user_id"); senderID != "" {
		contextData["sender_id"] = senderID
	}
	if event.MessageType == "group" {
		contextData["group_id"] = event.GroupID
	}
	if timestamp := quoteField(data, "time"); timestamp != "" {
		contextData["time"] = timestamp
	}
	return astrBotQuoteNode{Context: contextData, Segments: segments, ParentID: parentID}, true
}

func astrBotQuotePrompt(chain []astrBotQuoteNode) string {
	if len(chain) == 0 {
		return ""
	}
	messages := make([]map[string]any, 0, len(chain))
	for _, node := range chain {
		messages = append(messages, node.Context)
	}
	prompt, err := json.Marshal(map[string]any{"quoted_messages": messages})
	if err != nil {
		return ""
	}
	return string(prompt)
}
