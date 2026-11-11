package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func messageFingerprint(message core.ChatMessage) string {
	raw, _ := json.Marshal(struct {
		Content any
		Calls   []core.ToolCall
	}{message.Content, message.ToolCalls})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func stateMatches(message core.ChatMessage, format Format, model string) bool {
	state := message.ProviderState
	return state != nil && state.Format == string(format) && state.Model == model && state.Fingerprint == messageFingerprint(message)
}

func rememberState(message *core.ChatMessage, format Format, model string, content any) {
	raw, err := json.Marshal(content)
	if err != nil {
		return
	}
	message.ProviderState = &core.ProviderState{Format: string(format), Model: model, Content: raw, Fingerprint: messageFingerprint(*message)}
}
