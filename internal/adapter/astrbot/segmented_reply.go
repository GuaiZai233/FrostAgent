package astrbot

import "github.com/GuaiZai233/FrostAgent/internal/llm"

// AstrBot confirms transport writes rather than platform ACKs. Preserve the
// written prefix if a later write fails without recording any unsent suffix.
func recordPartialReply(session *llm.SessionContext, engine *llm.Engine, epoch uint64, event Event, text string) {
	if session == nil || engine == nil {
		return
	}
	log := llm.NewPublicReplyLog(session, epoch)
	log.Record(text, func(bool) {
		if event.MessageType == "group" {
			stageAssistantGroupMessage(session, engine, text, event.MessageID, nil)
		}
	})
	log.Finish(true)
	engine.TrimSession(session)
}
