package heartbeat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func socketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		accepted <- conn
		<-release
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	conn := <-accepted
	t.Cleanup(func() {
		_ = client.Close()
		_ = conn.Close()
		close(release)
	})
	return conn, client
}

func readPeer(conn *websocket.Conn) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return done
}

func TestPongKeepsIdleConnectionAlive(t *testing.T) {
	conn, peer := socketPair(t)
	r := start(context.Background(), conn, conn.Close, 25*time.Millisecond, 250*time.Millisecond, time.Second)
	t.Cleanup(r.Close)
	peerDone := readPeer(peer)
	t.Cleanup(func() { _ = peer.Close(); <-peerDone })
	result := make(chan error, 1)
	go func() {
		_, data, err := r.ReadMessage()
		if err == nil && string(data) != "alive" {
			err = errors.New("unexpected application message")
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("connection did not survive multiple Pong windows: %v", err)
	case <-time.After(650 * time.Millisecond):
	}
	if err := peer.WriteMessage(websocket.TextMessage, []byte("alive")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("application data was not delivered")
	}
}

func TestMissingPongExpiresDespiteApplicationHeartbeats(t *testing.T) {
	for _, sendData := range []bool{false, true} {
		name := "idle"
		if sendData {
			name = "application_heartbeats"
		}
		t.Run(name, func(t *testing.T) {
			conn, peer := socketPair(t)
			peer.SetPingHandler(func(string) error { return nil })
			peerDone := readPeer(peer)
			t.Cleanup(func() { _ = peer.Close(); <-peerDone })
			r := start(context.Background(), conn, conn.Close, 20*time.Millisecond, 150*time.Millisecond, time.Second)
			t.Cleanup(r.Close)
			if sendData {
				stop := make(chan struct{})
				done := make(chan struct{})
				go func() {
					defer close(done)
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-stop:
							return
						case <-ticker.C:
							if err := peer.WriteMessage(websocket.TextMessage, []byte(`{"type":"heartbeat"}`)); err != nil {
								return
							}
						}
					}
				}()
				t.Cleanup(func() { close(stop); _ = peer.Close(); <-done })
			}
			result := make(chan error, 1)
			go func() {
				for {
					if _, _, err := r.ReadMessage(); err != nil {
						result <- err
						return
					}
				}
			}()
			select {
			case err := <-result:
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("expected Pong timeout, got %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("missing Pong did not expire")
			}
		})
	}
}

func TestMessageProcessingDoesNotConsumePongBudget(t *testing.T) {
	conn, peer := socketPair(t)
	r := start(context.Background(), conn, conn.Close, 25*time.Millisecond, 200*time.Millisecond, time.Second)
	t.Cleanup(r.Close)
	peerDone := readPeer(peer)
	t.Cleanup(func() { _ = peer.Close(); <-peerDone })
	if err := peer.WriteMessage(websocket.TextMessage, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	// Simulate synchronous ingress processing that outlasts the read timeout.
	time.Sleep(450 * time.Millisecond)
	if err := peer.WriteMessage(websocket.TextMessage, []byte("second")); err != nil {
		t.Fatal(err)
	}
	_, data, err := r.ReadMessage()
	if err != nil || string(data) != "second" {
		t.Fatalf("healthy connection expired during server processing: %q, %v", data, err)
	}
}

func TestCancellationClosesSocketAndStopsWorker(t *testing.T) {
	conn, _ := socketPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := start(ctx, conn, conn.Close, time.Hour, time.Hour, time.Second)
	t.Cleanup(r.Close)
	result := make(chan error, 1)
	go func() { _, _, err := r.ReadMessage(); result <- err }()
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock the socket")
	}
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat worker did not exit")
	}
}

func TestPingWriteFailureStopsWorker(t *testing.T) {
	conn, _ := socketPair(t)
	r := start(context.Background(), conn, conn.Close, 10*time.Millisecond, time.Hour, time.Second)
	t.Cleanup(r.Close)
	_ = conn.Close()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat worker survived a failed Ping write")
	}
}
