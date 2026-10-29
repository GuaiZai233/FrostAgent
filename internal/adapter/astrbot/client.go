package astrbot

import (
	"context"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsclient"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/gorilla/websocket"
)

// ConfigureConnection installs an immutable snapshot before serving requests.
// The caller validates configuration before publishing this adapter.
func (a *Adapter) ConfigureConnection(config wsclient.Config) {
	a.connectionConfig = config
}

// RunClient owns the outbound connection loop for this adapter activation.
// Concurrent starts are ignored. CloseConnections permanently stops this
// activation, including an in-progress handshake or reconnect delay.
func (a *Adapter) RunClient(parent context.Context) {
	a.mu.Lock()
	if a.stopped || a.clientRunning || a.connectionConfig.Mode != wsclient.Forward {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	a.clientCancel, a.clientRunning = cancel, true
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.clientCancel, a.clientRunning = nil, false
		a.mu.Unlock()
	}()
	if a.engine != nil {
		done, ok := a.engine.Enter()
		if !ok {
			return
		}
		defer done()
		stop := context.AfterFunc(a.engine.Context(), cancel)
		defer stop()
	}
	wsclient.Run(ctx, a.connectionConfig, func(connection *websocket.Conn) {
		a.serveConnection(ctx, connection, false, "正向 WebSocket")
	}, func(message string) {
		if a.engine != nil {
			a.engine.Log().Warn(logs.WEBSOCKET, "astrbot: "+message)
		}
	})
}
