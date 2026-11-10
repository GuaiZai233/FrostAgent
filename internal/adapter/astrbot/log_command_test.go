package astrbot

import (
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func TestLogCommandPrivateDeliveryNeverUsesOriginalGroupQueue(t *testing.T) {
	for _, delivered := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[delivered], func(t *testing.T) {
			conn, actions, cleanup := setupTestAstrBotWS(t)
			defer cleanup()
			scope := newAdminTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: "synthetic-admin"})
			defer scope.Wait()
			defer scope.Cancel()
			scope.Logger = logs.New("test", "test", 20)
			conn.Scope = scope
			engine := &llm.Engine{Scope: scope}
			id := scope.Logger.Error(logs.LLM_RESPONSE, "sensitive endpoint.invalid detail")
			event := Event{Type: "event", EventType: "message", MessageID: "synthetic-msg", SessionID: "aiocqhttp:group:synthetic-group", Platform: "aiocqhttp", UserID: "synthetic-admin", GroupID: "synthetic-group", MessageType: "group", Content: "/log " + id}
			if !handleAdminCommand(conn, event, engine) {
				t.Fatal("bare log command not handled")
			}
			for i := 0; i < 2; i++ {
				select {
				case action := <-actions:
					if i == 0 {
						if action.MessageType != "private" || action.UserID != event.UserID || action.TargetID != event.UserID || action.GroupID != "" || action.Echo != "" || !strings.Contains(action.Content, id) || action.DeliveryID == "" {
							t.Fatalf("private log could enter group queue: %+v", action)
						}
						conn.acceptDeliveryReceipt(deliveryReceipt{DeliveryID: action.DeliveryID, Delivered: delivered, Error: "synthetic failure"})
					} else {
						if action.MessageType != "group" || action.Echo != "reply_"+event.MessageID || strings.Contains(action.Content, "endpoint.invalid") {
							t.Fatalf("unsafe group reply: %+v", action)
						}
						if delivered != strings.Contains(action.Content, "已私聊") {
							t.Fatalf("incorrect delivery result: %+v", action)
						}
					}
				case <-time.After(2 * time.Second):
					t.Fatal("log reply timeout")
				}
			}
			event.UserID = "synthetic-other"
			if !handleAdminCommand(conn, event, engine) {
				t.Fatal("nonadmin command not consumed")
			}
			select {
			case action := <-actions:
				if action.Action != "noop" || !action.SuppressLLM {
					t.Fatal("unauthorized details")
				}
			case <-time.After(time.Second):
				t.Fatal("missing suppression")
			}
		})
	}
}
