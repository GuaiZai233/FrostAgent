package onebot

import (
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"time"
)

// Preserve only acknowledged segments if a later segment fails or the turn is
// interrupted. This is a delivery fact, not a completed extraction turn.
func recordPartialReply(session *llm.SessionContext, engine *llm.Engine, epoch uint64, group bool, text string) {
	if session == nil || engine == nil {
		return
	}
	log := llm.NewPublicReplyLog(session, epoch)
	log.Record(text, func(bool) {
		if !group {
			return
		}
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
	log.Finish(true)
	engine.TrimSession(session)
}
