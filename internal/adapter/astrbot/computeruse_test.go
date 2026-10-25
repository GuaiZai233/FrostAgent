package astrbot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
	"github.com/gorilla/websocket"
)

type computerCommandProbe struct{ calls chan core.ComputerUseIngress }

func (p *computerCommandProbe) AuthenticateTransport(value string) string {
	if value == "Bearer synthetic-adapter-token" {
		return "synthetic-proof"
	}
	return ""
}
func (p *computerCommandProbe) HandleCommand(_ context.Context, v core.ComputerUseIngress) (bool, string) {
	p.calls <- v
	return true, ""
}
func TestComputerUseWebSocketRequiresTransportProof(t *testing.T) {
	for _, scenario := range []struct {
		name, token string
		mock        bool
		want        bool
	}{
		{"unverified", "", false, false}, {"wrong", "wrong", false, false}, {"trusted", "synthetic-adapter-token", false, true}, {"mock", "synthetic-adapter-token", true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			probe := &computerCommandProbe{make(chan core.ComputerUseIngress, 1)}
			scope := runtimescope.New(nil, nil, logs.New("synthetic-instance", "Synthetic", 20))
			defer scope.Cancel()
			engine := &llm.Engine{Scope: scope, ComputerUse: probe}
			adapter := NewAdapter(engine)
			server := httptest.NewServer(adapter.Handler())
			defer server.Close()
			defer adapter.CloseConnections()
			url := "ws" + strings.TrimPrefix(server.URL, "http")
			if scenario.mock {
				url += "?mock=true"
			}
			header := http.Header{}
			if scenario.token != "" {
				header.Set("Authorization", "Bearer "+scenario.token)
			}
			conn, _, err := websocket.DefaultDialer.Dial(url, header)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			event := Event{Type: "event", EventType: "message", MessageType: "private", Platform: "qq", UserID: "synthetic-owner", Content: "/computer start synthetic-project synthetic-instruction"}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			select {
			case input := <-probe.calls:
				if !scenario.want || input.TransportProof != "synthetic-proof" {
					t.Fatal("unauthenticated command reached controller")
				}
			case <-time.After(100 * time.Millisecond):
				if scenario.want {
					t.Fatal("trusted command not dispatched")
				}
			}
		})
	}
}
