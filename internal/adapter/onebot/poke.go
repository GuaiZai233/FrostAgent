package onebot

import (
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/poke"
	"time"
)

func (a *Adapter) normalizePoke(event model.OneBotEvent, mock bool) (model.OneBotEvent, bool) {
	if mock || a.engine == nil || a.engine.Getenv("ENABLE_POKE") != "true" ||
		event.PostType != "notice" || event.NoticeType != "notify" || event.SubType != "poke" ||
		event.SelfID <= 0 || event.TargetID != event.SelfID || event.UserID <= 0 ||
		event.UserID == event.SelfID || event.GroupID < 0 {
		return event, false
	}
	messageType := "private"
	if event.GroupID > 0 {
		messageType = "group"
	}
	if !a.engine.PokeGate.Ready(pokeConversation(event), time.Now(), poke.Cooldown(a.engine.Getenv)) {
		return event, false
	}
	message, _ := json.Marshal(poke.Message(a.engine.Getenv("POKE_CUSTOM_TEXT")))
	return model.OneBotEvent{
		SelfID: event.SelfID, PostType: "message", MessageType: messageType,
		GroupID: event.GroupID, UserID: event.UserID, Sender: event.Sender,
		Message: message, PokeID: poke.ID(),
	}, true
}

// admitPoke reserves cooldown only after ingress and dialogue eligibility pass.
func (a *Adapter) admitPoke(event model.OneBotEvent) bool {
	return a.engine.Getenv("ENABLE_POKE") == "true" &&
		a.engine.PokeGate.Allow(pokeConversation(event), time.Now(), poke.Cooldown(a.engine.Getenv))
}

func pokeConversation(event model.OneBotEvent) string {
	if event.GroupID > 0 {
		return fmt.Sprintf("qq:%d:group:%d", event.SelfID, event.GroupID)
	}
	return fmt.Sprintf("qq:%d:private:%d", event.SelfID, event.UserID)
}
