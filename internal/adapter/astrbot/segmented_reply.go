package astrbot

import "github.com/GuaiZai233/FrostAgent/internal/llm"

// Final progress already owns public history and the live transcript. Preserve
// its group-context projection on failure without automatic memory extraction.
func recordPartialReply(session *llm.SessionContext, engine *llm.Engine, epoch uint64, event Event, text string) {
	if session == nil || engine == nil {
		return
	}
	session.WithEpochCommit(epoch, func() {
		if event.MessageType == "group" {
			stageAssistantGroupMessage(session, engine, text, event.MessageID, nil)
		}
	})
	engine.TrimSession(session, astrBotRouteScope(event))
}
