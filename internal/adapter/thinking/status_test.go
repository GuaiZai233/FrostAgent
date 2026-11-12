package thinking

import (
	"context"
	"errors"
	"sync"
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
	for _, mode := range []string{"stop", "failure"} {
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

// Observe request cancellation before invoking Stop: cleanup must not hide a
// missing connection or turn cancellation binding.
func TestContextsCancelInFlightRequest(t *testing.T) {
	for _, mode := range []string{"connection", "turn"} {
		t.Run(mode, func(t *testing.T) {
			connection, cancelConnection := context.WithCancel(context.Background())
			defer cancelConnection()
			turn, cancelTurn := context.WithCancel(context.Background())
			defer cancelTurn()
			started := make(chan struct{})
			exited := make(chan struct{})
			stop := Start(connection, turn, time.Second, func() bool { return true }, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(exited)
				return ctx.Err()
			})
			defer stop()
			receive(t, started)
			if mode == "connection" {
				cancelConnection()
			} else {
				cancelTurn()
			}
			receive(t, exited)
		})
	}
}

func TestHotDisableStopsRefreshWithoutExplicitStop(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	calls := make(chan struct{}, 10)
	observedDisabled := make(chan struct{})
	var once sync.Once
	stop := Start(context.Background(), context.Background(), 5*time.Millisecond, func() bool {
		value := enabled.Load()
		if !value {
			once.Do(func() { close(observedDisabled) })
		}
		return value
	}, func(context.Context) error { calls <- struct{}{}; return nil })
	defer stop()
	receive(t, calls)
	enabled.Store(false)
	receive(t, observedDisabled)
	select {
	case <-calls:
		t.Fatal("notification after disabled setting was observed")
	case <-time.After(15 * time.Millisecond):
	}
}
