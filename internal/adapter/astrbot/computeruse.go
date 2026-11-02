package astrbot

import (
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

func handleComputerUseCommand(conn *wsConn, event Event, engine *llm.Engine) bool {
	if (event.Type != "" && event.Type != "event") || (event.EventType != "" && event.EventType != "message") {
		return false
	}
	fields := strings.Fields(event.Content)
	if len(fields) == 0 || fields[0] != "/computer" {
		return false
	}
	if conn == nil || conn.mock || conn.computerProof == "" || event.MessageType != "private" || event.pokeID != "" || isProactiveReply(&event) || engine == nil || engine.ComputerUse == nil {
		if conn != nil {
			_ = sendTerminalNoopWithSuppress(event, conn)
		}
		return true
	}
	input := core.ComputerUseIngress{Platform: event.Platform, UserID: event.UserID, MessageType: "private", Text: event.Content, TransportProof: conn.computerProof}
	engine.Go(func() {
		_, reply := engine.ComputerUse.HandleCommand(engine.Context(), input)
		if reply == "" {
			_ = sendTerminalNoopWithSuppress(event, conn)
		} else {
			_ = sendAstrBotAdminReply(event, conn, reply, false)
		}
	})
	return true
}
