// Package state binds opaque provider continuation data to its original message and route.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

type Origin struct {
	EndpointID string
	URL        string
}

func Fingerprint(message core.ChatMessage, origin Origin) string {
	raw, _ := json.Marshal(struct {
		Content any
		Calls   []core.ToolCall
		Origin  Origin
	}{message.Content, message.ToolCalls, origin})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func Matches(message core.ChatMessage, format, model string, origin Origin) bool {
	state := message.ProviderState
	return origin.URL != "" && state != nil && state.Format == format && state.Model == model && state.Fingerprint == Fingerprint(message, origin)
}

func Remember(message *core.ChatMessage, format, model string, content any, origin Origin) {
	if origin.URL == "" {
		return
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return
	}
	message.ProviderState = &core.ProviderState{Format: format, Model: model, Content: raw, Fingerprint: Fingerprint(*message, origin)}
}
