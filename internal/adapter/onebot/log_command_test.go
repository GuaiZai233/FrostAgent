package onebot

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
)

func TestLogCommandWithoutMentionSendsDetailsPrivately(t *testing.T) {
	// Small synthetic identities are transport fixtures, not real QQ accounts.
	const callerID int64 = 202
	conn, actions, cleanup := setupTestWS(t)
	defer cleanup()
	scope := newAdminTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: strconv.FormatInt(callerID, 10)})
	defer scope.Wait()
	defer scope.Cancel()
	scope.Logger = logs.New("test", "test", 20)
	conn.Scope = scope
	engine := &llm.Engine{Scope: scope}
	id := scope.Logger.Error(logs.LLM_RESPONSE, "sensitive endpoint.invalid detail")
	previousID := scope.Logger.Error(logs.SYSTEM, "unrelated preceding error")
	text, _ := json.Marshal("/log " + id)
	event := model.OneBotEvent{SelfID: 101, UserID: callerID, GroupID: 303, MessageType: "group", Message: text}
	if !handleAdminCommand(conn, event, engine, completedPreviousError(previousID)) {
		t.Fatal("bare log command not handled")
	}
	for i := 0; i < 2; i++ {
		select {
		case action := <-actions:
			params, ok := action.Params.(map[string]any)
			if !ok {
				t.Fatalf("unexpected action parameters: %+v", action.Params)
			}
			body, _ := json.Marshal(params["message"])
			if i == 0 {
				if action.Action != "send_private_msg" || !strings.Contains(string(body), id) || !strings.Contains(string(body), "endpoint.invalid") || params["user_id"] != float64(callerID) {
					t.Fatalf("wrong private destination: %+v", action)
				}
			} else if action.Action != "send_group_msg" || strings.Contains(string(body), "endpoint.invalid") || !strings.Contains(string(body), "已私聊") {
				t.Fatalf("unsafe group reply: %+v", action)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("log reply timeout")
		}
	}
	event.UserID = 404
	if !handleAdminCommand(conn, event, engine, nil) {
		t.Fatal("nonadmin command not consumed")
	}
	select {
	case action := <-actions:
		t.Fatalf("nonadmin received action: %+v", action)
	default:
	}
	reset, _ := json.Marshal("/reset")
	event.Message = reset
	if _, candidate, _ := extractOneBotAdminCommand(event, "/", scope); candidate {
		t.Fatal("other commands lost mention requirement")
	}
}
