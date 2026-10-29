package wsclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	handshakeTimeout = 10 * time.Second
	initialRetry     = time.Second
	maximumRetry     = 30 * time.Second
	stableConnection = 30 * time.Second
)

// Run maintains one outbound connection until ctx is canceled. serve owns the
// established connection's read loop and must return after its socket closes.
// report receives only sanitized status text, never URLs, tokens or peer bodies.
func Run(ctx context.Context, config Config, serve func(*websocket.Conn), report func(string)) {
	run(ctx, config, serve, report, initialRetry, maximumRetry)
}

func run(ctx context.Context, config Config, serve func(*websocket.Conn), report func(string), initial, maximum time.Duration) {
	if config.Mode != Forward || ctx.Err() != nil {
		return
	}
	if err := config.Validate(); err != nil {
		if report != nil {
			report(err.Error())
		}
		return
	}
	retry := initial
	for ctx.Err() == nil {
		attempt, cancel := context.WithCancel(ctx)
		dialer := websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: handshakeTimeout,
			NetDialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
				connection, err := (&net.Dialer{}).DialContext(dialCtx, network, address)
				if err == nil {
					// Gorilla's handshake timeout bounds I/O, but cancellation of
					// its context alone does not interrupt HTTP response reads.
					context.AfterFunc(attempt, func() { _ = connection.Close() })
				}
				return connection, err
			},
		}
		headers := make(http.Header)
		if config.Token != "" {
			headers.Set("Authorization", "Bearer "+config.Token)
		}
		connection, response, err := dialer.DialContext(attempt, config.URL, headers)
		if err != nil {
			if ctx.Err() == nil && report != nil {
				if response != nil {
					report(fmt.Sprintf("WebSocket handshake rejected (HTTP %d); retrying", response.StatusCode))
				} else {
					report("WebSocket connection failed; retrying")
				}
			}
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
		} else {
			connectedAt := time.Now()
			if ctx.Err() == nil {
				serve(connection)
			}
			_ = connection.Close()
			if time.Since(connectedAt) >= stableConnection {
				retry = initial
			}
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		retry = min(retry*2, maximum)
	}
}
