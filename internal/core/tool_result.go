package core

import "context"

// ToolExecutionResult separates model-visible content from execution metadata.
// MemoryWritten describes a completed durable write. MessagePayload is a
// requested delivery, whose success still depends on the adapter acknowledgement.
type ToolExecutionResult struct {
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
}

// StructuredToolExecutor is optional; existing string-returning tools remain valid.
type StructuredToolExecutor interface {
	ExecuteResult(context.Context, string) (ToolExecutionResult, error)
}
