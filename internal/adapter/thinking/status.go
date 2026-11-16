// Package thinking manages best-effort, request-scoped typing notifications.
package thinking

import (
	"context"
	"time"
)

const EnvKey = "ENABLE_THINKING_STATUS"
const Interval = 4 * time.Second

// Start refreshes a platform's temporary input status until the run ends, either
// context is canceled, the setting is disabled, or delivery fails. Stop joins
// the worker so a late notification cannot follow the final reply.
func Start(connection, turn context.Context, interval time.Duration, enabled func() bool, send func(context.Context) error) func() {
	if !enabled() || connection.Err() != nil || turn.Err() != nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(connection)
	detach := context.AfterFunc(turn, cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil && enabled() {
			if err := send(ctx); err != nil {
				return
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return func() {
		cancel()
		detach()
		<-done
	}
}
