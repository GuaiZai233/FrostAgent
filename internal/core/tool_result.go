package core

import "context"

// ToolExecutionResult separates model-visible content from execution metadata.
// MemoryWritten describes a completed durable write. MessagePayload is a
// requested delivery, whose success still depends on the adapter acknowledgement.
type ToolExecutionResult struct {
	HistoryRestricted bool
	// RawResult preserves the logical MCP response before display formatting.
	RawResult      string
	HistoryRefID   string
	Content        string
	MemoryWritten  bool
	MessagePayload string
	// PlatformActionPayload is prepared by a native tool and dispatched only
	// after the same security checks as message delivery.
	PlatformActionPayload string
}

type PlatformActionResult struct {
	Content      string
	MessagesSent int
	StickerSent  bool
	// ExternalMessagesSent counts sends outside the originating conversation.
	ExternalMessagesSent int
	// Incomplete reports that at least one requested item was not confirmed. The
	// successful parts are still counted in MessagesSent.
	Incomplete bool
}

// StructuredToolExecutor is optional; existing string-returning tools remain valid.
type StructuredToolExecutor interface {
	ExecuteResult(context.Context, string) (ToolExecutionResult, error)
}
