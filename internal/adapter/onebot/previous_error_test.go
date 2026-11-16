package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

func completedPreviousError(id string) *previousErrorReply {
	reply := &previousErrorReply{uuid: id, done: make(chan struct{}), sent: true}
	close(reply.done)
	return reply
}

func previousErrorID(t *testing.T, reply *previousErrorReply) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	id, active := reply.resolve(ctx, ctx)
	if !active {
		t.Fatal("previous error did not resolve")
	}
	return id
}

func recordSentPreviousError(conn *wsConnection, data []byte) {
	key, reply := conn.beginOutgoingError(data)
	conn.finishOutgoingError(key, reply, nil)
}

func readLogAction(t *testing.T, conn *websocket.Conn) (model.OneBotAction, string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var action model.OneBotAction
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	params, ok := action.Params.(map[string]any)
	if !ok {
		t.Fatalf("unexpected parameters: %+v", action)
	}
	raw, _ := json.Marshal(params["message"])
	text, _ := historyMessageText(raw)
	return action, text
}

func TestBareLogAfterErrorOverWebSocket(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			// Synthetic transport identities, not real accounts.
			const callerID int64 = 202
			scope := newAdminTestScope(t, map[string]string{
				admincmd.AdminQQIDsEnv:   strconv.FormatInt(callerID, 10),
				"ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true",
			})
			defer scope.Wait()
			defer scope.Cancel()
			scope.Logger = logs.New("test", "test", 20)
			engine := &llm.Engine{Scope: scope}
			adapter := NewAdapter(engine)
			srv := httptest.NewServer(http.HandlerFunc(adapter.Handler()))
			defer srv.Close()
			defer adapter.CloseConnections()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			event := model.OneBotEvent{PostType: "message", SelfID: 101, UserID: callerID,
				GroupID: 303, MessageType: kind, MessageID: 1,
				Message: json.RawMessage(`[{"type":"at","data":{"qq":"101"}},{"type":"text","data":{"text":"/reflect"}}]`),
			}
			if kind == "private" {
				event.Message = json.RawMessage(`"/reflect"`)
			}
			if err := client.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			errorAction, failure := readLogAction(t, client)
			_, reference, found := strings.Cut(failure, " (Ref_ID: ")
			id, valid := logs.NormalizeErrorUUID(strings.TrimSuffix(reference, ")"))
			if !found || !valid || !strings.Contains(failure, "[FrostAgent 错误]") {
				t.Fatalf("expected genuine bot failure: %q", failure)
			}
			if err := client.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": errorAction.Echo}); err != nil {
				t.Fatal(err)
			}
			event.Message = json.RawMessage(`"/log"`)
			event.MessageID++
			if err := client.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			action, detail := readLogAction(t, client)
			if action.Action != "send_private_msg" || !strings.Contains(detail, id) || !strings.Contains(detail, "记忆反思未启用或不可用") {
				t.Fatalf("missing private diagnostics: %+v %q", action, detail)
			}
			if kind == "group" {
				if err := client.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo}); err != nil {
					t.Fatal(err)
				}
				groupAction, receipt := readLogAction(t, client)
				if groupAction.Action != "send_group_msg" || !strings.Contains(receipt, "已私聊") || strings.Contains(receipt, id) {
					t.Fatalf("unsafe group receipt: %q", receipt)
				}
			}
			// The lookup's ordinary reply replaces the error as the preceding message.
			event.MessageID++
			if err := client.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			_, usage := readLogAction(t, client)
			if !strings.Contains(usage, admincmd.FormatUsage("/")) || strings.Contains(usage, "格式错误") || strings.Contains(usage, id) {
				t.Fatalf("expected only command directory: %q", usage)
			}
		})
	}
}

func TestPreviousErrorRequiresAdjacentBotMessage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		intervene string
		wantError bool
	}{
		{"adjacent", "", true},
		{"other-group", "other-group", true},
		{"other-private", "other-private", true},
		{"API-request", "API-request", true},
		{"notice", "notice", true},
		{"current-error-echo", "error-echo", true},
		{"other-bot-message", "message-sent", false},
		{"late-error-echo", "late-echo", false},
		{"user-message", "user", false},
		{"spoofed-user-error", "spoof", false},
		{"bot-text", "bot", false},
		{"bot-media", "media", false},
		{"bot-forward", "forward", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, actions, cleanup := setupTestWS(t)
			defer cleanup()
			store := logs.New("test", "test", 20)
			failure := store.ReportError(logs.SYSTEM, "synthetic failure", nil)
			event := model.OneBotEvent{PostType: "message", SelfID: 101, UserID: 202, GroupID: 303, MessageType: "group"}
			write := func(action string, params map[string]any) {
				t.Helper()
				data, _ := json.Marshal(model.OneBotAction{Action: action, Params: params})
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					t.Fatal(err)
				}
				<-actions
			}
			write("send_group_msg", map[string]any{"group_id": event.GroupID, "message": failure})
			switch tc.intervene {
			case "other-group":
				other := event
				other.GroupID++
				conn.takePreviousError(other)
			case "other-private":
				other := event
				other.MessageType = "private"
				conn.takePreviousError(other)
			case "notice":
				other := event
				other.PostType = "notice"
				conn.takePreviousError(other)
			case "user", "spoof":
				other := event
				other.UserID++
				message := "ordinary user message"
				if tc.intervene == "spoof" {
					message = failure
				}
				other.Message, _ = json.Marshal(message)
				conn.takePreviousError(other)
			case "error-echo", "message-sent", "late-echo":
				if tc.intervene == "late-echo" {
					conn.takePreviousError(event)
				}
				echo := event
				echo.PostType, echo.UserID = "message_sent", event.SelfID
				message := failure
				if tc.intervene == "message-sent" {
					message = "ordinary message from another bot client"
				}
				echo.Message, _ = json.Marshal(message)
				if id := previousErrorID(t, conn.takePreviousError(echo)); id != "" {
					t.Fatalf("message_sent became a command input: %q", id)
				}
			case "bot":
				write("send_group_msg", map[string]any{"group_id": event.GroupID, "message": "ordinary bot message"})
			case "media":
				write("send_group_msg", map[string]any{"group_id": event.GroupID, "message": []map[string]any{{"type": "image", "data": map[string]any{"file": "synthetic.png"}}}})
			case "forward":
				write("send_group_forward_msg", map[string]any{"group_id": event.GroupID, "messages": []any{}})
			case "API-request":
				write("get_group_info", map[string]any{"group_id": event.GroupID})
			}
			id := previousErrorID(t, conn.takePreviousError(event))
			if tc.wantError != (id != "") || (id != "" && !strings.Contains(failure, id)) {
				t.Fatalf("wrong preceding error: %q", id)
			}
			if next := previousErrorID(t, conn.takePreviousError(event)); next != "" {
				t.Fatalf("stale error survived next incoming message: %q", next)
			}
		})
	}
}

func TestBareLogUsageAndPermissions(t *testing.T) {
	for _, prefix := range []string{"/", "!", "execute"} {
		t.Run(prefix, func(t *testing.T) {
			conn, actions, cleanup := setupTestWS(t)
			defer cleanup()
			scope := newAdminTestScope(t, map[string]string{
				admincmd.AdminQQIDsEnv: "202", admincmd.AdminCommandPrefixEnv: prefix,
			})
			defer scope.Wait()
			defer scope.Cancel()
			scope.Logger = logs.New("test", "test", 20)
			conn.Scope = scope
			engine := &llm.Engine{Scope: scope}
			command := prefix + admincmd.PrefixSeparator(prefix) + "log"
			raw, _ := json.Marshal(command)
			event := model.OneBotEvent{SelfID: 101, UserID: 202, MessageType: "private", Message: raw}
			if !handleAdminCommand(conn, event, engine, nil) {
				t.Fatal("bare log was not handled")
			}
			select {
			case action := <-actions:
				params := action.Params.(map[string]any)
				if text, _ := params["message"].(string); text != admincmd.FormatUsage(prefix) {
					t.Fatalf("expected command directory only: %+v", action)
				}
			case <-time.After(time.Second):
				t.Fatal("command directory timeout")
			}
			event.UserID = 404
			id := scope.Logger.Error(logs.SYSTEM, "private diagnostic")
			if !handleAdminCommand(conn, event, engine, completedPreviousError(id)) {
				t.Fatal("nonadmin command not consumed")
			}
			select {
			case action := <-actions:
				t.Fatalf("nonadmin received output: %+v", action)
			default:
			}
		})
	}
}

func TestPreviousErrorPrivateMessageSent(t *testing.T) {
	for _, mode := range []string{"current-error-echo", "ordinary-bot-message", "late-error-echo"} {
		t.Run(mode, func(t *testing.T) {
			conn := newWSConnection(nil)
			failure := logs.New("test", "test", 20).ReportError(logs.SYSTEM, "synthetic failure", nil)
			data, _ := json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": failure}})
			recordSentPreviousError(conn, data)
			event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 101, UserID: 202}
			if mode == "late-error-echo" {
				conn.takePreviousError(event)
			}
			message := failure
			if mode == "ordinary-bot-message" {
				message = "ordinary message from another bot client"
			}
			raw, _ := json.Marshal(message)
			echo := model.OneBotEvent{PostType: "message_sent", MessageType: "private", SelfID: 101, UserID: 101, TargetID: 202, Message: raw}
			if id := previousErrorID(t, conn.takePreviousError(echo)); id != "" {
				t.Fatalf("message_sent became a command input: %q", id)
			}
			id := previousErrorID(t, conn.takePreviousError(event))
			if (id != "") != (mode == "current-error-echo") {
				t.Fatalf("wrong preceding error after %s: %q", mode, id)
			}
		})
	}
}

func TestPreviousErrorIgnoresFailedWritesAndBoundsState(t *testing.T) {
	conn := newWSConnection(nil)
	store := logs.New("test", "test", 20)
	failure := store.ReportError(logs.SYSTEM, "synthetic failure", nil)
	data, _ := json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": failure}})
	if err := conn.WriteMessage(websocket.TextMessage, data); err == nil {
		t.Fatal("expected failed write")
	}
	if len(conn.previousErrors) != 0 {
		t.Fatal("failed write created a preceding bot message")
	}
	for target := 1; target <= maxTrustedMessageSessions+1; target++ {
		data := []byte(fmt.Sprintf(`{"action":"send_private_msg","params":{"user_id":%d,"message":%q}}`, target, failure))
		recordSentPreviousError(conn, data)
	}
	if len(conn.previousErrors) != maxTrustedMessageSessions {
		t.Fatalf("unbounded preceding errors: %d", len(conn.previousErrors))
	}
	if len(conn.outgoingErrorEchoes) != maxTrustedMessageSessions || len(conn.errorEchoOrder) != maxTrustedMessageSessions {
		t.Fatal("unbounded error echo deduplication")
	}
}

func TestPreviousErrorEchoCacheBoundsAndSessionIsolation(t *testing.T) {
	conn := newWSConnection(nil)
	store := logs.New("test", "test", 20)
	failure := store.ReportError(logs.SYSTEM, "synthetic failure", nil)
	for target, text := range map[int]string{202: failure, 303: store.ReportError(logs.SYSTEM, "another failure", nil)} {
		data, _ := json.Marshal(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": target, "message": text}})
		recordSentPreviousError(conn, data)
	}
	echo := model.OneBotEvent{PostType: "message_sent", MessageType: "private", SelfID: 101, UserID: 101, TargetID: 303,
		Message: mustMarshalPreviousErrorMessage(t, failure)}
	conn.takePreviousError(echo)
	if conn.previousErrors["private:303"] != nil || conn.previousErrors["private:202"] == nil {
		t.Fatal("another conversation's known UUID suppressed an unknown echo")
	}
	conn = newWSConnection(nil)
	for i := 0; i <= maxTrustedMessageSessions; i++ {
		conn.rememberErrorEcho("private:202", fmt.Sprintf("synthetic-error-%d", i))
	}
	if len(conn.outgoingErrorEchoes) != maxTrustedMessageSessions || len(conn.errorEchoOrder) != maxTrustedMessageSessions {
		t.Fatal("same-conversation error echoes are unbounded")
	}
	if _, exists := conn.outgoingErrorEchoes[outgoingErrorEcho{session: "private:202", uuid: "synthetic-error-0"}]; exists {
		t.Fatal("oldest error echo was not evicted")
	}
	latest := outgoingErrorEcho{session: "private:202", uuid: fmt.Sprintf("synthetic-error-%d", maxTrustedMessageSessions)}
	if _, exists := conn.outgoingErrorEchoes[latest]; !exists {
		t.Fatal("latest error echo was not retained")
	}
	before := conn.nextErrorEcho
	conn.rememberErrorEcho(latest.session, latest.uuid)
	if conn.nextErrorEcho != before {
		t.Fatal("duplicate error echo evicted another entry")
	}
}
