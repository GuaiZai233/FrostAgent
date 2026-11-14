package onebot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

type errorWriteReturnGate struct {
	enabled atomic.Bool
	wrote   chan struct{}
	release chan struct{}
}

type errorGatedConn struct {
	net.Conn
	gate *errorWriteReturnGate
}

func (c *errorGatedConn) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	if err == nil && bytes.Contains(data, []byte("[FrostAgent 错误]")) && c.gate.enabled.CompareAndSwap(true, false) {
		close(c.gate.wrote)
		<-c.gate.release
	}
	return n, err
}

type errorGatedListener struct {
	net.Listener
	gate *errorWriteReturnGate
}

func (l errorGatedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &errorGatedConn{Conn: conn, gate: l.gate}, nil
}

func TestBareLogBeforeErrorWriteReturns(t *testing.T) {
	scope := newAdminTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: "202"})
	defer scope.Wait()
	defer scope.Cancel()
	scope.Logger = logs.New("test", "test", 20)
	adapter := NewAdapter(&llm.Engine{Scope: scope})
	gate := &errorWriteReturnGate{wrote: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	srv := httptest.NewUnstartedServer(adapter.Handler())
	srv.Listener = errorGatedListener{Listener: srv.Listener, gate: gate}
	srv.Start()
	defer srv.Close()
	defer adapter.CloseConnections()
	defer releaseOnce.Do(func() { close(gate.release) })
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gate.enabled.Store(true)
	event := model.OneBotEvent{PostType: "message", SelfID: 101, UserID: 202, MessageType: "private", Message: json.RawMessage(`"/reflect"`)}
	if err := client.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	errorAction, failure := readLogAction(t, client)
	select {
	case <-gate.wrote:
	case <-time.After(time.Second):
		t.Fatal("error write was not gated")
	}
	adapter.mu.RLock()
	var serverConn *wsConnection
	for conn := range adapter.conns {
		serverConn = conn
	}
	adapter.mu.RUnlock()
	if serverConn == nil {
		t.Fatal("missing adapter connection")
	}
	serverConn.previousErrorMu.Lock()
	pending := serverConn.previousErrors["private:202"]
	serverConn.previousErrorMu.Unlock()
	if pending == nil || !strings.Contains(failure, pending.uuid) {
		t.Fatal("received error has no pending UUID")
	}
	if err := client.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": errorAction.Echo}); err != nil {
		t.Fatal(err)
	}
	event.Message = json.RawMessage(`"/log"`)
	if err := client.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		serverConn.previousErrorMu.Lock()
		taken := serverConn.previousErrors["private:202"] == nil
		serverConn.previousErrorMu.Unlock()
		if taken {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("log ingress waited for socket write to return")
		}
		time.Sleep(time.Millisecond)
	}
	releaseOnce.Do(func() { close(gate.release) })
	_, detail := readLogAction(t, client)
	if !strings.Contains(detail, pending.uuid) || !strings.Contains(detail, "Bot 错误信息") {
		t.Fatalf("immediate log lost received error: %q", detail)
	}
}

func TestPreviousErrorPendingCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		conn := newWSConnection(nil)
		failure := logs.New("test", "test", 20).ReportError(logs.SYSTEM, "synthetic failure", nil)
		data, _ := json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": failure}})
		key, pending := conn.beginOutgoingError(data)
		event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 101, UserID: 202}
		taken := conn.takePreviousError(event)
		var writeErr error
		if fail {
			writeErr = errors.New("synthetic write failure")
		}
		conn.finishOutgoingError(key, pending, writeErr)
		id := previousErrorID(t, taken)
		if (id != "") == fail || (id != "" && !strings.Contains(failure, id)) {
			t.Fatalf("wrong write result: fail=%v id=%q", fail, id)
		}
		if next := conn.takePreviousError(event); next != nil {
			t.Fatal("completion revived a consumed error")
		}
	}
}

func TestPreviousErrorPendingCancellationAndFailedOrdinaryMessage(t *testing.T) {
	conn := newWSConnection(nil)
	failure := logs.New("test", "test", 20).ReportError(logs.SYSTEM, "synthetic failure", nil)
	data, _ := json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": failure}})
	recordSentPreviousError(conn, data)
	data, _ = json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": "ordinary message"}})
	key, pending := conn.beginOutgoingError(data)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var absent *previousErrorReply
	if _, active := absent.resolve(ctx, context.Background()); active {
		t.Fatal("canceled instance resolved an absent candidate")
	}
	if _, active := absent.resolve(context.Background(), ctx); active {
		t.Fatal("closed connection resolved an absent candidate")
	}
	if _, active := pending.resolve(ctx, context.Background()); active {
		t.Fatal("canceled instance kept waiting for write")
	}
	if _, active := pending.resolve(context.Background(), ctx); active {
		t.Fatal("closed connection kept waiting for write")
	}
	conn.finishOutgoingError(key, pending, errors.New("synthetic write failure"))
	event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 101, UserID: 202}
	if id := previousErrorID(t, conn.takePreviousError(event)); id == "" || !strings.Contains(failure, id) {
		t.Fatal("failed ordinary write discarded the preceding error")
	}
}
