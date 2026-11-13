package onebot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

func setupPreviousErrorACKTest(t *testing.T) (*wsConnection, <-chan model.OneBotAction, func(model.OneBotAction, string, int)) {
	t.Helper()
	actions := make(chan model.OneBotAction, 10)
	connected := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer wire.Close()
		connected <- wire
		for {
			var action model.OneBotAction
			if wire.ReadJSON(&action) != nil {
				return
			}
			actions <- action
		}
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	conn := newWSConnection(client)
	wire := <-connected
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := client.ReadMessage()
			if err != nil {
				return
			}
			conn.handleAPIResponse(data)
		}
	}()
	t.Cleanup(func() { conn.Close(); <-readDone; srv.Close() })
	ack := func(action model.OneBotAction, status string, code int) {
		t.Helper()
		if err := wire.WriteJSON(map[string]any{"status": status, "retcode": code, "echo": action.Echo}); err != nil {
			t.Fatal(err)
		}
	}
	return conn, actions, ack
}

func readPreviousErrorAction(t *testing.T, actions <-chan model.OneBotAction) model.OneBotAction {
	t.Helper()
	select {
	case action := <-actions:
		return action
	case <-time.After(2 * time.Second):
		t.Fatal("outbound action timeout")
		return model.OneBotAction{}
	}
}

func sendPreviousErrorFixture(t *testing.T, conn *wsConnection, actions <-chan model.OneBotAction, text string) {
	t.Helper()
	data, _ := json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": text}})
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatal(err)
	}
	readPreviousErrorAction(t, actions)
}

func TestPreviousErrorLogUsesPlatformACKResult(t *testing.T) {
	for _, outcome := range []string{"success", "rejected", "missing-status", "timeout"} {
		for _, message := range []string{"ordinary", "ordinary-with-error-echo", "error", "error-without-predecessor"} {
			t.Run(outcome+"/"+message, func(t *testing.T) {
				conn, actions, ack := setupPreviousErrorACKTest(t)
				scope := newAdminTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: "202"})
				defer scope.Wait()
				defer scope.Cancel()
				scope.Logger = logs.New("test", "test", 20)
				conn.Scope = scope
				engine := &llm.Engine{Scope: scope}
				previous := scope.Logger.ReportError(logs.SYSTEM, "previous diagnostic", nil)
				newError := scope.Logger.ReportError(logs.SYSTEM, "new diagnostic", nil)
				if message != "error-without-predecessor" {
					sendPreviousErrorFixture(t, conn, actions, previous)
				}
				text := newError
				ordinary := strings.HasPrefix(message, "ordinary")
				if ordinary {
					text = "ordinary reply"
				}
				timeout := time.Second
				if outcome == "timeout" {
					timeout = 50 * time.Millisecond
				}
				sent := make(chan error, 1)
				go func() {
					_, err := conn.SendActionAndWait(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": text}}, timeout)
					sent <- err
				}()
				action := readPreviousErrorAction(t, actions)
				event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 101, UserID: 202, Message: json.RawMessage(`"/log"`)}
				if message == "ordinary-with-error-echo" {
					raw, _ := json.Marshal(previous)
					echo := model.OneBotEvent{PostType: "message_sent", MessageType: "private", SelfID: 101, UserID: 101, TargetID: 202, Message: raw}
					if conn.takePreviousError(echo) != nil {
						t.Fatal("error echo became a command input")
					}
				}
				conn.previousErrorMu.Lock()
				pending := conn.previousErrors["private:202"]
				conn.previousErrorMu.Unlock()
				if pending == nil {
					t.Fatal("socket write prematurely discarded pending delivery")
				}
				select {
				case <-pending.done:
					t.Fatal("socket write prematurely completed platform delivery")
				default:
				}
				startLog := func() {
					t.Helper()
					if !handleAdminCommand(conn, event, engine, conn.takePreviousError(event)) {
						t.Fatal("log command not consumed")
					}
				}
				if outcome != "rejected" {
					startLog()
				}
				switch outcome {
				case "success":
					ack(action, "ok", 0)
				case "rejected":
					ack(action, "failed", 1400)
				case "missing-status":
					ack(action, "", 0)
				}
				select {
				case err := <-sent:
					if (err == nil) != (outcome == "success") {
						t.Fatalf("unexpected delivery result: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("delivery did not complete")
				}
				if outcome == "rejected" {
					startLog()
				}
				reply := readPreviousErrorAction(t, actions)
				body, _ := json.Marshal(reply.Params)
				want := admincmd.FormatUsage("/")
				if outcome == "success" && !ordinary {
					want = "new diagnostic"
				} else if outcome != "success" && message != "error-without-predecessor" {
					want = "previous diagnostic"
				}
				params := reply.Params.(map[string]any)
				actual, _ := params["message"].(string)
				if reply.Action != "send_private_msg" || !strings.Contains(actual, want) {
					t.Fatalf("incorrect log result: %s", body)
				}
				if outcome != "success" && strings.Contains(actual, "new diagnostic") {
					t.Fatal("lookup exposed rejected error output")
				}
			})
		}
	}
}

func TestPreviousErrorOutOfOrderACKs(t *testing.T) {
	for _, firstOK := range []bool{false, true} {
		for _, secondOK := range []bool{false, true} {
			conn, actions, ack := setupPreviousErrorACKTest(t)
			store := logs.New("test", "test", 20)
			previous := store.ReportError(logs.SYSTEM, "previous", nil)
			first := store.ReportError(logs.SYSTEM, "first", nil)
			second := store.ReportError(logs.SYSTEM, "second", nil)
			sendPreviousErrorFixture(t, conn, actions, previous)
			start := func(text string) (model.OneBotAction, <-chan error) {
				result := make(chan error, 1)
				go func() {
					_, err := conn.SendActionAndWait(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": text}}, time.Second)
					result <- err
				}()
				return readPreviousErrorAction(t, actions), result
			}
			action1, result1 := start(first)
			action2, result2 := start(second)
			event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 101, UserID: 202}
			pending := conn.takePreviousError(event)
			complete := func(action model.OneBotAction, result <-chan error, accepted bool) {
				t.Helper()
				status, code := "failed", 1400
				if accepted {
					status, code = "ok", 0
				}
				ack(action, status, code)
				select {
				case err := <-result:
					if (err == nil) != accepted {
						t.Fatalf("wrong ACK result: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("ACK completion timeout")
				}
			}
			complete(action2, result2, secondOK)
			complete(action1, result1, firstOK)
			want := previous
			if firstOK {
				want = first
			}
			if secondOK {
				want = second
			}
			id := previousErrorID(t, pending)
			if id == "" || !strings.Contains(want, id) {
				t.Fatalf("ACK order changed message order: first=%v second=%v id=%q", firstOK, secondOK, id)
			}
			if conn.takePreviousError(event) != nil {
				t.Fatal("late ACK revived consumed state")
			}
		}
	}
}
