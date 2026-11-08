package astrbot

import "strings"

const maxReplyTargets = 4096

// Include the transport destination as well as the session key: a supplied
// session_id must not make messages from another platform or group quotable.
type replyTargetKey struct {
	Session, Platform, MessageType, Peer, MessageID string
}

func (c *wsConn) replyTargetKey(event Event, messageID string) replyTargetKey {
	peer := event.UserID
	if event.MessageType == "group" {
		peer = event.GroupID
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	return replyTargetKey{c.sessionKey(event), platform, event.MessageType, peer, strings.TrimSpace(messageID)}
}

func (c *wsConn) rememberReplyTarget(event Event) {
	if c == nil || strings.TrimSpace(event.MessageID) == "" ||
		(event.Type != "" && event.Type != "event") ||
		(event.EventType != "" && event.EventType != "message") ||
		(event.MessageType != "group" && event.MessageType != "private") {
		return
	}
	key := c.replyTargetKey(event, event.MessageID)
	c.replyTargetsMu.Lock()
	defer c.replyTargetsMu.Unlock()
	if c.replyTargets == nil {
		c.replyTargets = make(map[replyTargetKey]struct{})
	}
	if _, exists := c.replyTargets[key]; exists {
		return
	}
	c.replyTargets[key] = struct{}{}
	c.replyTargetOrder = append(c.replyTargetOrder, key)
	if len(c.replyTargetOrder) > maxReplyTargets {
		delete(c.replyTargets, c.replyTargetOrder[0])
		c.replyTargetOrder = c.replyTargetOrder[1:]
	}
}

func (c *wsConn) observedReplyTarget(event Event, messageID string) bool {
	if c == nil || strings.TrimSpace(messageID) == "" {
		return false
	}
	key := c.replyTargetKey(event, messageID)
	c.replyTargetsMu.Lock()
	defer c.replyTargetsMu.Unlock()
	_, ok := c.replyTargets[key]
	return ok
}
