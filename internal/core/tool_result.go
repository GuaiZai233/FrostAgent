package core

import "context"

// ToolExecutionResult separates model-visible content from execution metadata.
// MemoryWritten describes a completed durable write. MessagePayload is a
// requested delivery, whose success still depends on the adapter acknowledgement.
type ToolExecutionResult struct {
	Content        string
	MemoryWritten  bool
	MessagePayload string
}

// StructuredToolExecutor is optional; existing string-returning tools remain valid.
type StructuredToolExecutor interface {
	ExecuteResult(context.Context, string) (ToolExecutionResult, error)
}
