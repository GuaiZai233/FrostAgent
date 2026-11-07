package onebot

import (
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"time"
)

// Final progress already owns public history and the live transcript. Preserve
// its group-context projection on failure without automatic memory extraction.
func recordPartialReply(session *llm.SessionContext, engine *llm.Engine, epoch uint64, group bool, text string) {
	if session == nil || engine == nil {
		return
	}
	session.WithEpochCommit(epoch, func() {
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
	engine.TrimSession(session)
}
