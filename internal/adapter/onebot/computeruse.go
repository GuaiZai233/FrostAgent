package onebot

import (
	"strconv"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

func handleComputerUseCommand(conn *wsConnection, event model.OneBotEvent, engine *llm.Engine) bool {
	if event.PostType != "message" {
		return false
	}
	var text strings.Builder
	for _, segment := range ParseMessageSegments(event.Message) {
		if segment.Type == "text" {
			if value, ok := segment.Data["text"].(string); ok {
				text.WriteString(value)
			}
		}
	}
	fields := strings.Fields(text.String())
	if len(fields) == 0 || fields[0] != "/computer" {
		return false
	}
	// Intercept before history, quote expansion, security content logs or
	// ActionsCat dispatch, including commands from forbidden mock/group input.
	if conn == nil || conn.mock || conn.computerProof == "" || event.MessageType != "private" || event.PokeID != "" || event.UserID <= 0 || engine == nil || engine.ComputerUse == nil {
		return true
	}
	input := core.ComputerUseIngress{Platform: "onebot", UserID: strconv.FormatInt(event.UserID, 10), MessageType: "private", Text: text.String(), TransportProof: conn.computerProof}
	engine.Go(func() {
		_, reply := engine.ComputerUse.HandleCommand(engine.Context(), input)
		if reply != "" {
			sendOneBotReply(event, conn, reply)
		}
	})
	return true
}
