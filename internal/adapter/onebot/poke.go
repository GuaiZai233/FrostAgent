package onebot

import (
	"FrostAgent/internal/model"
	"FrostAgent/internal/poke"
	"encoding/json"
	"fmt"
	"time"
)

func (a *Adapter) normalizePoke(event model.OneBotEvent, mock bool) (model.OneBotEvent, bool) {
	if mock || a.engine == nil || a.engine.Getenv("ENABLE_POKE") != "true" ||
		event.PostType != "notice" || event.NoticeType != "notify" || event.SubType != "poke" ||
		event.SelfID <= 0 || event.TargetID != event.SelfID || event.UserID <= 0 ||
		event.UserID == event.SelfID || event.GroupID < 0 {
		return event, false
	}
	conversation := fmt.Sprintf("qq:%d:private:%d", event.SelfID, event.UserID)
	messageType := "private"
	if event.GroupID > 0 {
		conversation = fmt.Sprintf("qq:%d:group:%d", event.SelfID, event.GroupID)
		messageType = "group"
	}
	if !a.engine.PokeGate.Allow(conversation, time.Now(), poke.Cooldown(a.engine.Getenv)) {
		return event, false
	}
	message, _ := json.Marshal(poke.Message(a.engine.Getenv("POKE_CUSTOM_TEXT")))
	return model.OneBotEvent{
		SelfID: event.SelfID, PostType: "message", MessageType: messageType,
		GroupID: event.GroupID, UserID: event.UserID, Sender: event.Sender,
		Message: message, PokeID: poke.ID(),
	}, true
}
