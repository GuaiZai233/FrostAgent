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

func sendPreviousErrorFixture(t *testing.T, conn *wsConnection, actions <-chan model.OneBotAction, ack func(model.OneBotAction, string, int), text string) {
	t.Helper()
	data, _ := json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": text}})
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatal(err)
	}
	ack(readPreviousErrorAction(t, actions), "ok", 0)
	conn.previousErrorMu.Lock()
	pending := conn.previousErrors["private:202"]
	conn.previousErrorMu.Unlock()
	if id := previousErrorID(t, pending); id == "" || !strings.Contains(text, id) {
		t.Fatal("fixture error was not confirmed")
	}
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
					sendPreviousErrorFixture(t, conn, actions, ack, previous)
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
			sendPreviousErrorFixture(t, conn, actions, ack, previous)
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

func waitPreviousErrorCompletion(t *testing.T, pending *previousErrorReply) {
	t.Helper()
	if pending == nil {
		t.Fatal("missing delivery candidate")
	}
	select {
	case <-pending.done:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery completion timeout")
	}
}

func TestPreviousErrorDirectReplyUsesPlatformACKResult(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		for _, outcome := range []string{"success", "rejected", "missing-status", "timeout"} {
			for _, message := range []string{"ordinary", "error", "error-without-predecessor"} {
				t.Run(kind+"/"+outcome+"/"+message, func(t *testing.T) {
					conn, actions, ack := setupPreviousErrorACKTest(t)
					timeout := "2s"
					if outcome == "timeout" {
						timeout = "100ms"
					}
					scope := newAdminTestScope(t, map[string]string{
						admincmd.AdminQQIDsEnv: "202", "ONEBOT_ACTION_TIMEOUT": timeout,
					})
					defer scope.Wait()
					defer scope.Cancel()
					scope.Logger = logs.New("test", "test", 20)
					conn.Scope = scope
					engine := &llm.Engine{Scope: scope}
					event := model.OneBotEvent{PostType: "message", MessageType: kind, SelfID: 101, UserID: 202, GroupID: 303, Message: json.RawMessage(`"/log"`)}
					actionName, targetKey, target := "send_private_msg", "user_id", "202"
					if kind == "group" {
						actionName, targetKey, target = "send_group_msg", "group_id", "303"
					}
					previous := scope.Logger.ReportError(logs.SYSTEM, "previous diagnostic", nil)
					if message != "error-without-predecessor" {
						data, _ := json.Marshal(model.OneBotAction{Action: actionName, Params: map[string]any{targetKey: target, "message": previous}})
						recordSentPreviousError(conn, data)
					}
					text := scope.Logger.ReportError(logs.SYSTEM, "new diagnostic", nil)
					if message == "ordinary" {
						text = "ordinary reply"
					}
					// This returns before ACK, as it must also be safe in the reader.
					sendDirectReply(actionName, targetKey, target, "echo_direct", event, conn, text)
					action := readPreviousErrorAction(t, actions)
					pending := conn.takePreviousError(event)
					if pending == nil {
						t.Fatal("socket write discarded the delivery candidate")
					}
					select {
					case <-pending.done:
						t.Fatal("direct reply completed without platform confirmation")
					default:
					}
					if !handleAdminCommand(conn, event, engine, pending) {
						t.Fatal("log command not consumed")
					}
					switch outcome {
					case "success":
						ack(action, "ok", 0)
					case "rejected":
						ack(action, "failed", 1400)
					case "missing-status":
						ack(action, "", 0)
					}
					waitPreviousErrorCompletion(t, pending)
					if pending.sent != (outcome == "success") {
						t.Fatalf("wrong direct reply result: sent=%v", pending.sent)
					}
					reply := readPreviousErrorAction(t, actions)
					body, _ := json.Marshal(reply.Params)
					want := admincmd.FormatUsage("/")
					if outcome == "success" && message != "ordinary" {
						want = "new diagnostic"
					} else if outcome != "success" && message != "error-without-predecessor" {
						want = "previous diagnostic"
					}
					params := reply.Params.(map[string]any)
					actual, _ := historyMessageText(mustMarshalPreviousErrorMessage(t, params["message"]))
					if !strings.Contains(actual, want) {
						t.Fatalf("incorrect direct reply lookup: %s", body)
					}
					if outcome != "success" && strings.Contains(actual, "new diagnostic") {
						t.Fatal("lookup exposed an unconfirmed direct reply")
					}
					ack(reply, "ok", 0)
				})
			}
		}
	}
}

func mustMarshalPreviousErrorMessage(t *testing.T, message any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPreviousErrorDelayedEchoAfterNewErrorACK(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		for _, firstACK := range []string{"before-new-error", "after-late-echo", "consumed-before-new-error"} {
			t.Run(kind+"/"+firstACK, func(t *testing.T) {
				conn, actions, ack := setupPreviousErrorACKTest(t)
				scope := newAdminTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: "202"})
				defer scope.Wait()
				defer scope.Cancel()
				scope.Logger = logs.New("test", "test", 20)
				conn.Scope = scope
				engine := &llm.Engine{Scope: scope}
				event := model.OneBotEvent{PostType: "message", MessageType: kind, SelfID: 101, UserID: 202, GroupID: 303, Message: json.RawMessage(`"/log"`)}
				actionName, targetKey, target := "send_private_msg", "user_id", "202"
				if kind == "group" {
					actionName, targetKey, target = "send_group_msg", "group_id", "303"
				}
				first := scope.Logger.ReportError(logs.SYSTEM, "old diagnostic", nil)
				second := scope.Logger.ReportError(logs.SYSTEM, "latest diagnostic", nil)
				sendDirectReply(actionName, targetKey, target, "echo_direct", event, conn, first)
				firstAction := readPreviousErrorAction(t, actions)
				conn.previousErrorMu.Lock()
				firstPending := conn.previousErrors[historyKey(event)]
				conn.previousErrorMu.Unlock()
				if firstACK != "after-late-echo" {
					ack(firstAction, "ok", 0)
					waitPreviousErrorCompletion(t, firstPending)
				}
				if firstACK == "consumed-before-new-error" {
					conn.takePreviousError(event)
				}
				sendDirectReply(actionName, targetKey, target, "echo_direct", event, conn, second)
				secondAction := readPreviousErrorAction(t, actions)
				if secondAction.Echo == firstAction.Echo {
					t.Fatal("concurrent direct replies reused an ACK echo")
				}
				conn.previousErrorMu.Lock()
				secondPending := conn.previousErrors[historyKey(event)]
				conn.previousErrorMu.Unlock()
				ack(secondAction, "ok", 0)
				waitPreviousErrorCompletion(t, secondPending)
				if secondPending.previous != nil {
					t.Fatal("confirmed error retained the lookup fallback chain")
				}
				echo := event
				echo.PostType, echo.UserID, echo.TargetID = "message_sent", event.SelfID, event.UserID
				echo.Message = mustMarshalPreviousErrorMessage(t, first)
				if conn.takePreviousError(echo) != nil {
					t.Fatal("delayed echo became a command input")
				}
				if firstACK == "after-late-echo" {
					ack(firstAction, "ok", 0)
					waitPreviousErrorCompletion(t, firstPending)
				}
				pending := conn.takePreviousError(event)
				if id := previousErrorID(t, pending); id == "" || !strings.Contains(second, id) {
					t.Fatal("delayed old error echo cleared the latest error")
				}
				if !handleAdminCommand(conn, event, engine, pending) {
					t.Fatal("log command not consumed")
				}
				reply := readPreviousErrorAction(t, actions)
				body, _ := json.Marshal(reply.Params)
				if !strings.Contains(string(body), "latest diagnostic") || strings.Contains(string(body), "old diagnostic") {
					t.Fatalf("log did not query the latest error: %s", body)
				}
				ack(reply, "ok", 0)
			})
		}
	}
}

func TestPreviousErrorDirectReplyCancelledOnClose(t *testing.T) {
	conn, actions, _ := setupPreviousErrorACKTest(t)
	failure := logs.New("test", "test", 20).ReportError(logs.SYSTEM, "unconfirmed", nil)
	event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 101, UserID: 202}
	sendDirectReply("send_private_msg", "user_id", "202", "echo_direct", event, conn, failure)
	readPreviousErrorAction(t, actions)
	pending := conn.takePreviousError(event)
	conn.Close()
	waitPreviousErrorCompletion(t, pending)
	if pending.sent {
		t.Fatal("connection closure confirmed an undelivered error")
	}
	conn.messageMu.Lock()
	defer conn.messageMu.Unlock()
	if len(conn.pendingMessage) != 0 {
		t.Fatal("connection closure retained a raw send ACK waiter")
	}
}
