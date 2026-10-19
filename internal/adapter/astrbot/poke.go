package astrbot

import (
	"FrostAgent/internal/adapter/parity"
	"FrostAgent/internal/poke"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func positivePokeID(raw string) bool {
	id, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == raw
}

func (a *Adapter) normalizePoke(event Event, mock bool) (Event, bool) {
	if mock || a.engine == nil || a.engine.Getenv("ENABLE_POKE") != "true" ||
		event.Type != "event" || event.EventType != "notice" ||
		event.NoticeType != "notify" || event.SubType != "poke" ||
		parity.CanonicalBillingPlatform(event.Platform) != "qq" ||
		!positivePokeID(event.SelfID) || event.TargetID != event.SelfID ||
		!positivePokeID(event.UserID) || event.UserID == event.SelfID ||
		strings.TrimSpace(event.MessageID) == "" ||
		(event.MessageType != "group" && event.MessageType != "private") ||
		(event.MessageType == "group" && !positivePokeID(event.GroupID)) ||
		(event.MessageType == "private" && event.GroupID != "") {
		return event, false
	}
	conversation := fmt.Sprintf("qq:%s:private:%s", event.SelfID, event.UserID)
	if event.MessageType == "group" {
		conversation = fmt.Sprintf("qq:%s:group:%s", event.SelfID, event.GroupID)
	}
	if !a.engine.PokeGate.Allow(conversation, time.Now(), poke.Cooldown(a.engine.Getenv)) {
		return event, false
	}
	return Event{
		Type: "event", EventType: "message", MessageID: event.MessageID,
		Platform: event.Platform, MessageType: event.MessageType,
		GroupID: event.GroupID, UserID: event.UserID, SenderName: event.SenderName,
		SenderCard: event.SenderCard, GroupName: event.GroupName,
		Content: poke.Message(a.engine.Getenv("POKE_CUSTOM_TEXT")),
		IsWake:  true, Timestamp: event.Timestamp, pokeID: poke.ID(),
		StickerDeliveryReceipt: event.StickerDeliveryReceipt,
		BotLoopInterrupted:     event.BotLoopInterrupted,
	}, true
}

func (event Event) replyMessageID() string {
	if event.pokeID != "" {
		return ""
	}
	return event.MessageID
}
