package thinking

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func receive(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("status worker did not progress")
	}
}

func TestStatusLifecycle(t *testing.T) {
	for _, mode := range []string{"stop", "connection", "turn", "disabled", "failure"} {
		t.Run(mode, func(t *testing.T) {
			conn, cancelConn := context.WithCancel(context.Background())
			defer cancelConn()
			turn, cancelTurn := context.WithCancel(context.Background())
			defer cancelTurn()
			var enabled atomic.Bool
			enabled.Store(true)
			calls := make(chan struct{}, 10)
			stop := Start(conn, turn, 5*time.Millisecond, enabled.Load, func(ctx context.Context) error {
				calls <- struct{}{}
				if mode == "failure" {
					return errors.New("unsupported")
				}
				return nil
			})
			defer stop()
			receive(t, calls)
			if mode != "failure" {
				receive(t, calls)
			}
			switch mode {
			case "connection":
				cancelConn()
			case "turn":
				cancelTurn()
			case "disabled":
				enabled.Store(false)
			}
			stop()
			stop() // Cleanup may be both explicit and deferred.
			for len(calls) > 0 {
				<-calls
			}
			select {
			case <-calls:
				t.Fatal("notification after stop")
			case <-time.After(15 * time.Millisecond):
			}
		})
	}
}

func TestStopCancelsInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	stop := Start(context.Background(), context.Background(), time.Second, func() bool { return true }, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	receive(t, started)
	stop()
	receive(t, exited)
}

func TestDisabledAndCanceledDoNotStart(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx     context.Context
		enabled bool
	}{{context.Background(), false}, {canceled, true}} {
		stop := Start(tc.ctx, context.Background(), time.Second, func() bool { return tc.enabled }, func(context.Context) error { t.Error("unexpected status"); return nil })
		stop()
	}
}
