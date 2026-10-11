// Package heartbeat provides connection-scoped WebSocket liveness checks.
package heartbeat

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pingInterval = 30 * time.Second
	pongTimeout  = 90 * time.Second
	writeTimeout = 10 * time.Second
)

// Reader must be used by the connection's sole reader. Its timeout budget counts
// time spent reading, excluding synchronous application processing between reads.
type Reader struct {
	conn      *websocket.Conn
	timeout   time.Duration
	remaining time.Duration
	deadline  time.Time
	cancel    context.CancelFunc
	done      chan struct{}
}

// Start sends periodic Ping control frames until Close or parent cancellation.
// closeConnection must close the socket and cancel its connection-owned work.
func Start(parent context.Context, conn *websocket.Conn, closeConnection func() error) *Reader {
	return start(parent, conn, closeConnection, pingInterval, pongTimeout, writeTimeout)
}

func start(parent context.Context, conn *websocket.Conn, closeConnection func() error, interval, timeout, writeWait time.Duration) *Reader {
	ctx, cancel := context.WithCancel(parent)
	r := &Reader{conn: conn, timeout: timeout, remaining: timeout, cancel: cancel, done: make(chan struct{})}
	// Gorilla runs this callback in the reader goroutine.
	conn.SetPongHandler(func(string) error {
		r.deadline = time.Now().Add(r.timeout)
		return conn.SetReadDeadline(r.deadline)
	})
	go func() {
		defer close(r.done)
		defer closeConnection()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// WriteControl is safe concurrently with the adapter's data writer.
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
					return
				}
			}
		}
	}()
	return r
}

// ReadMessage preserves the remaining Pong budget across data frames. A peer
// cannot keep the connection alive merely by sending application heartbeats.
func (r *Reader) ReadMessage() (int, []byte, error) {
	r.deadline = time.Now().Add(r.remaining)
	if err := r.conn.SetReadDeadline(r.deadline); err != nil {
		return 0, nil, err
	}
	kind, data, err := r.conn.ReadMessage()
	r.remaining = time.Until(r.deadline)
	return kind, data, err
}

// Close unblocks socket I/O and waits for the heartbeat worker to exit.
func (r *Reader) Close() {
	r.cancel()
	_ = r.conn.Close()
	<-r.done
}
