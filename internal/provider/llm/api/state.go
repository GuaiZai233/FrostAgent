package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

type stateOrigin struct {
	EndpointID string
	URL        string
}

func messageFingerprint(message core.ChatMessage, origin stateOrigin) string {
	raw, _ := json.Marshal(struct {
		Content any
		Calls   []core.ToolCall
		Origin  stateOrigin
	}{message.Content, message.ToolCalls, origin})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func stateMatches(message core.ChatMessage, format Format, model string, origin stateOrigin) bool {
	state := message.ProviderState
	return origin.URL != "" && state != nil && state.Format == string(format) && state.Model == model && state.Fingerprint == messageFingerprint(message, origin)
}

func rememberState(message *core.ChatMessage, format Format, model string, content any, origin stateOrigin) {
	if origin.URL == "" {
		return
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return
	}
	message.ProviderState = &core.ProviderState{Format: string(format), Model: model, Content: raw, Fingerprint: messageFingerprint(*message, origin)}
}
