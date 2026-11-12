// Package chatflow defines the in-process boundary between chat transports and
// conversation processing. Transports own protocol delivery; the processor owns
// security, routing, session ordering, tools and memory.
package chatflow

import (
	"context"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
)

type Input struct {
	Message core.IncomingMessage
	IsWake  bool
	IsAt    bool
	// Quote must come from a transport-validated reply in the same chat.
	Quote *core.IncomingMessage
}

type Part struct {
	Type          string
	Text          string
	MentionUserID string
	MessageID     string
	Path          string
	URL           string
	IsSticker     bool
}

type Delivery struct {
	Action  string
	Message core.OutgoingMessage
	Parts   []Part
}

// Sender returns success only after the platform confirms delivery.
type Sender func(context.Context, Delivery) error

type Processor interface {
	// Submit preserves intake order. Completion includes ignored messages and
	// failures, and is used by polling transports before confirming an update.
	Submit(Input) <-chan struct{}
	Close()
}

// DeliveryError carries confirmed delivery facts before a later physical part
// failed. Conversation history must keep the text prefix, while Confirmed also
// covers media-only sends.
type DeliveryError struct {
	Err           error
	DeliveredText string
	Confirmed     bool
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("message partially delivered; do not repeat confirmed parts: %v", e.Err)
}
func (e *DeliveryError) Unwrap() error { return e.Err }

// DeliveryConfirmation exposes confirmed parts to consumers without requiring
// them to depend on the transport's error type.
func (e *DeliveryError) DeliveryConfirmation() (string, bool) {
	return e.DeliveredText, e.Confirmed || e.DeliveredText != ""
}
