package backup

import "github.com/GuaiZai233/FrostAgent/internal/storage"

// ModulePayloads has a fixed envelope. Payload bytes, including fields unknown
// to this binary, are never decoded or rewritten during a backup round trip.
type ModulePayloads struct {
	ProtocolVersion int                     `json:"protocol_version"`
	Records         []storage.ModulePayload `json:"records"`
}
