package wsclient

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func awaitClientStop(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not stop promptly")
	}
}

func TestClientReconnectsAndAuthenticatesWithoutDisclosingSecrets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	var wrongAuth atomic.Bool
	reports := make(chan string, 10)
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-client-token" {
			wrongAuth.Store(true)
		}
		attempt := attempts.Add(1)
		if attempt == 1 {
			http.Error(w, "synthetic-client-token synthetic-query-secret", http.StatusUnauthorized)
			return
		}
		upgrader := websocket.Upgrader{}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		if attempt == 2 {
			return
		}
		_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, message, err := connection.ReadMessage()
		if err == nil {
			received <- string(message)
			_ = connection.WriteMessage(websocket.TextMessage, message)
		}
		<-ctx.Done()
	}))
	defer server.Close()
	done := make(chan struct{})
	echo := make(chan string, 1)
	go func() {
		defer close(done)
		run(ctx, Config{Mode: Forward, URL: "ws" + strings.TrimPrefix(server.URL, "http") + "/?token=synthetic-query-secret", Token: "synthetic-client-token"}, func(c *websocket.Conn) {
			_ = c.WriteMessage(websocket.TextMessage, []byte("synthetic-payload"))
			_, message, err := c.ReadMessage()
			if err == nil {
				echo <- string(message)
				_, _, _ = c.ReadMessage()
			}
		}, func(message string) { reports <- message }, 5*time.Millisecond, 20*time.Millisecond)
	}()
	defer func() { cancel(); awaitClientStop(t, done) }()
	select {
	case message := <-echo:
		if message != "synthetic-payload" {
			t.Fatalf("bad echo: %q", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not reconnect and exchange messages")
	}
	if attempts.Load() != 3 || wrongAuth.Load() {
		t.Fatalf("unexpected attempts/auth: %d %v", attempts.Load(), wrongAuth.Load())
	}
	if message := <-received; message != "synthetic-payload" {
		t.Fatal("upstream did not receive the payload")
	}
	select {
	case report := <-reports:
		if strings.Contains(report, "synthetic-") || !strings.Contains(report, "401") {
			t.Fatalf("unsanitized failure report: %q", report)
		}
	default:
		t.Fatal("handshake rejection was not reported")
	}
}

func TestClientCancellationInterruptsHandshake(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(released)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, Config{Mode: Forward, URL: "ws" + strings.TrimPrefix(server.URL, "http")}, func(*websocket.Conn) { t.Error("unexpected successful handshake") }, nil)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handshake never started")
	}
	cancel()
	awaitClientStop(t, done)
	awaitClientStop(t, released)
}

func TestClientCancellationInterruptsRetryWait(t *testing.T) {
	var attempts atomic.Int32
	reported := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx, Config{Mode: Forward, URL: "ws" + strings.TrimPrefix(server.URL, "http")}, func(*websocket.Conn) {}, func(string) { reported <- struct{}{} }, time.Hour, time.Hour)
	}()
	select {
	case <-reported:
	case <-time.After(3 * time.Second):
		t.Fatal("failure was not observed")
	}
	cancel()
	awaitClientStop(t, done)
	if attempts.Load() != 1 {
		t.Fatalf("unexpected reconnect after cancellation: %d", attempts.Load())
	}
}

func TestClientRetainsTLSVerification(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted TLS reached application handler")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reports := 0
	Run(ctx, Config{Mode: Forward, URL: "wss" + strings.TrimPrefix(server.URL, "https")}, func(*websocket.Conn) { t.Error("accepted untrusted TLS") }, func(string) { reports++; cancel() })
	if reports != 1 {
		t.Fatal("TLS rejection was not reported")
	}
}

func TestClientDoesNotFollowRedirectsOrDialInactiveConfig(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Run(ctx, Config{Mode: Forward, URL: "ws" + strings.TrimPrefix(redirect.URL, "http"), Token: "synthetic-secret"}, func(*websocket.Conn) { t.Error("unexpected connection") }, func(string) { cancel() })
	Run(context.Background(), Config{Mode: Reverse, URL: "ws" + strings.TrimPrefix(target.URL, "http")}, func(*websocket.Conn) {}, nil)
	Run(ctx, Config{Mode: Forward, URL: "ws" + strings.TrimPrefix(target.URL, "http")}, func(*websocket.Conn) {}, nil)
	Run(context.Background(), Config{Mode: Forward, URL: "http://invalid.example/"}, func(*websocket.Conn) {}, nil)
	if requests.Load() != 0 {
		t.Fatal("redirect/inactive configuration initiated an upstream request")
	}
}
