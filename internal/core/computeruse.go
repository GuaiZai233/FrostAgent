package core

import "context"

// ComputerUseIngress is constructed only by authenticated adapter transport
// code, before normal history, quote expansion, dispatch, or content logging.
// It is not a model tool schema or a management mock-chat identity.
type ComputerUseIngress struct {
	Platform, UserID, MessageType, Text string
	Mock, Proactive                     bool
	TransportProof                      string
}
type ComputerUseCommands interface {
	AuthenticateTransport(authorization string) string
	HandleCommand(context.Context, ComputerUseIngress) (handled bool, reply string)
}
