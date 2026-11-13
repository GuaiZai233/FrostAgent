package onebot

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

type previousErrorReply struct {
	uuid     string
	previous *previousErrorReply
	done     chan struct{}
	sent     bool // Published by closing done.
}

func (r *previousErrorReply) resolve(ctx, transportCtx context.Context) (string, bool) {
	if ctx.Err() != nil || transportCtx.Err() != nil {
		return "", false
	}
	for r != nil {
		select {
		case <-ctx.Done():
			return "", false
		case <-transportCtx.Done():
			return "", false
		case <-r.done:
		}
		if ctx.Err() != nil || transportCtx.Err() != nil {
			return "", false
		}
		if r.sent {
			return r.uuid, true
		}
		r = r.previous
	}
	return "", true
}

// retainedPreviousError drops completed failures without waiting for pending ACKs.
func retainedPreviousError(reply *previousErrorReply) *previousErrorReply {
	for reply != nil {
		select {
		case <-reply.done:
			if reply.sent {
				if reply.uuid == "" {
					return nil
				}
				return reply
			}
			reply = reply.previous
		default:
			return reply
		}
	}
	return nil
}

// takePreviousError snapshots the preceding message before the current message
// invalidates it, without waiting for a potentially blocked socket write.
func (c *wsConnection) takePreviousError(event model.OneBotEvent) *previousErrorReply {
	if c == nil || c.mock || (event.PostType != "message" && event.PostType != "message_sent") ||
		(event.MessageType != "private" && event.MessageType != "group") {
		return nil
	}
	if event.PostType == "message_sent" && event.MessageType == "private" {
		data := oneBotMessageData{UserID: event.UserID, Sender: event.Sender}
		if event.TargetID != 0 {
			data.TargetID = event.TargetID
		}
		peer, known := privateMessagePeer(event.SelfID, data)
		if !known || peer <= 0 {
			return nil
		}
		event.UserID = peer
	}
	c.previousErrorMu.Lock()
	defer c.previousErrorMu.Unlock()
	key := historyKey(event)
	reply := c.previousErrors[key]
	if event.PostType == "message_sent" {
		if uuid := outgoingErrorUUID(event.Message); uuid != "" {
			// A delayed echo may refer to an earlier error while the latest send
			// awaits its ACK. The same lock protects successful chain pruning.
			for known := reply; known != nil; known = known.previous {
				if known.uuid == uuid {
					return nil
				}
			}
		}
	}
	delete(c.previousErrors, key)
	if event.PostType == "message_sent" {
		return nil // Echoes may invalidate state, but never revive an older error.
	}
	return reply
}

// beginOutgoingError runs under writeMu before the socket write. A peer can
// receive bytes and reply before WriteMessage returns, so expose a pending result.
func (c *wsConnection) beginOutgoingError(data []byte) (string, *previousErrorReply) {
	if c.mock {
		return "", nil
	}
	var action struct {
		Action string `json:"action"`
		Params struct {
			UserID  json.RawMessage `json:"user_id"`
			GroupID json.RawMessage `json:"group_id"`
			Message json.RawMessage `json:"message"`
		} `json:"params"`
	}
	if json.Unmarshal(data, &action) != nil {
		return "", nil
	}
	switch action.Action {
	case "send_msg", "send_group_msg", "send_private_msg", "send_group_forward_msg", "send_private_forward_msg", "send_forward_msg", "forward_group_single_msg", "forward_friend_single_msg":
	default:
		return "", nil
	}
	kind, target := "private", historyRawID(action.Params.UserID)
	if group := historyRawID(action.Params.GroupID); group != "" && group != "0" {
		kind, target = "group", group
	}
	id, err := strconv.ParseInt(target, 10, 64)
	if err != nil || id <= 0 {
		return "", nil
	}
	c.previousErrorMu.Lock()
	defer c.previousErrorMu.Unlock()
	key := kind + ":" + strconv.FormatInt(id, 10)
	uuid := outgoingErrorUUID(action.Params.Message)
	previous := retainedPreviousError(c.previousErrors[key])
	if uuid == "" && previous == nil {
		return "", nil
	}
	if c.previousErrors == nil {
		c.previousErrors = make(map[string]*previousErrorReply)
	}
	// Keep transient transport state bounded even for many inactive conversations.
	if _, exists := c.previousErrors[key]; !exists && len(c.previousErrors) >= maxTrustedMessageSessions {
		for oldKey := range c.previousErrors {
			delete(c.previousErrors, oldKey)
			break
		}
	}
	// Earlier sends may still be awaiting ACKs; preserve their result rather than
	// copying a UUID that has not yet been confirmed by the platform.
	reply := &previousErrorReply{uuid: uuid, previous: previous, done: make(chan struct{})}
	c.previousErrors[key] = reply
	return key, reply
}

func (c *wsConnection) finishOutgoingError(key string, reply *previousErrorReply, err error) {
	if reply == nil {
		return
	}
	c.previousErrorMu.Lock()
	reply.sent = err == nil
	if reply.sent {
		reply.previous = nil // A confirmed message supersedes all earlier sends.
	}
	close(reply.done)
	if c.previousErrors[key] == reply {
		if previous := retainedPreviousError(reply); previous != nil {
			c.previousErrors[key] = previous
		} else {
			delete(c.previousErrors, key)
		}
	}
	c.previousErrorMu.Unlock()
}

func outgoingErrorUUID(raw json.RawMessage) string {
	var text strings.Builder
	for _, segment := range ParseMessageSegments(raw) {
		switch segment.Type {
		case "text":
			value, _ := segment.Data["text"].(string)
			text.WriteString(value)
		case "at", "reply":
			// Group reply decorations are not part of the error text.
		default:
			return ""
		}
	}
	message := strings.TrimSpace(text.String())
	if !strings.HasPrefix(message, "[FrostAgent 错误] ") || !strings.HasSuffix(message, ")") {
		return ""
	}
	_, reference, found := strings.Cut(message, " (Ref_ID: ")
	if !found {
		return ""
	}
	uuid, _ := logs.NormalizeErrorUUID(strings.TrimSuffix(reference, ")"))
	return uuid
}
