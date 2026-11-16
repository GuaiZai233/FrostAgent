package api

import (
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/state"
)

type stateOrigin = state.Origin

func stateMatches(message core.ChatMessage, format Format, model string, origin stateOrigin) bool {
	return state.Matches(message, string(format), model, origin)
}

func rememberState(message *core.ChatMessage, format Format, model string, content any, origin stateOrigin) {
	state.Remember(message, string(format), model, content, origin)
}
