package onebot

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/admincmd"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

func TestPrivateAdminCommandWithoutMention(t *testing.T) {
	for _, prefix := range []string{"/", "!", "execute"} {
		for _, command := range []string{"reset", "ban 404", "unban 404", "compact", "reflect", "log"} {
			t.Run(prefix+command, func(t *testing.T) {
				text := prefix + admincmd.PrefixSeparator(prefix) + command
				raw, _ := json.Marshal(text)
				// Small synthetic identities are transport fixtures, not real accounts.
				event := model.OneBotEvent{SelfID: 101, UserID: 202, MessageType: "private", Message: raw}
				cmd, candidate, err := extractOneBotAdminCommand(event, prefix, nil)
				if !candidate || err != nil || string(cmd.Type) != strings.Fields(command)[0] {
					t.Fatalf("private command not recognized: %+v, %v, %v", cmd, candidate, err)
				}
				event.MessageType, event.GroupID = "group", 303
				_, groupCandidate, _ := extractOneBotAdminCommand(event, prefix, nil)
				if groupCandidate != (cmd.Type == admincmd.CmdLog) {
					t.Fatalf("group mention requirement changed: %+v, %v", cmd, groupCandidate)
				}
			})
		}
	}
	for _, tc := range []struct {
		text      string
		candidate bool
		wantError bool
	}{
		{"reset", false, false},
		{"你好", false, false},
		{"请执行 /reset", false, false},
		{"/unknown", true, true},
		{"/ban", true, true},
		{"/reset extra", true, true},
	} {
		t.Run(tc.text, func(t *testing.T) {
			raw, _ := json.Marshal(tc.text)
			event := model.OneBotEvent{SelfID: 101, UserID: 202, MessageType: "private", Message: raw}
			_, candidate, err := extractOneBotAdminCommand(event, "/", nil)
			if candidate != tc.candidate || (err != nil) != tc.wantError {
				t.Fatalf("wrong candidate/error: %v, %v", candidate, err)
			}
		})
	}
}

func TestPrivateAdminCommandExecutionAndAccess(t *testing.T) {
	for _, role := range []string{"admin", "nonadmin", "locked-admin"} {
		t.Run(role, func(t *testing.T) {
			conn, actions, cleanup := setupTestWS(t)
			defer cleanup()
			scope := newAdminTestScope(t, map[string]string{admincmd.AdminQQIDsEnv: "202"})
			defer scope.Wait()
			defer scope.Cancel()
			conn.Scope = scope
			controller := security.NewController(t.TempDir())
			engine := &llm.Engine{Scope: scope, Security: controller, SessionManager: llm.NewSessionManager()}
			event := model.OneBotEvent{SelfID: 101, UserID: 202, MessageType: "private"}
			if role == "nonadmin" {
				event.UserID = 303
			}
			admin, err := security.NewPrincipal("onebot", "202")
			if err != nil {
				t.Fatal(err)
			}
			if role == "locked-admin" {
				if err := controller.Lock(admin, "synthetic lock"); err != nil {
					t.Fatal(err)
				}
			}
			commands := []struct{ text, reply string }{
				{"/reset", "当前会话已重置。"},
				{"/compact", "当前会话没有需要压缩的内容。"},
				{"/reflect", "[FrostAgent 错误] 管理员指令执行失败"},
				{"/ban 404", "已成功封禁用户 404。"},
				{"/unban 404", "已成功解封用户 404。"},
				{"/log", admincmd.FormatUsage("/")},
				{"/unknown", "未知指令：unknown"},
			}
			for _, command := range commands {
				event.Message, _ = json.Marshal(command.text)
				if !handleAdminCommand(conn, event, engine, nil) {
					t.Fatalf("private command not consumed: %s", command.text)
				}
				if role == "nonadmin" {
					select {
					case action := <-actions:
						t.Fatalf("nonadmin received output: %+v", action)
					default:
					}
					continue
				}
				select {
				case action := <-actions:
					params := action.Params.(map[string]any)
					text, _ := params["message"].(string)
					want := command.reply
					if role == "locked-admin" {
						want = security.RejectGatewayMsg
					}
					if action.Action != "send_private_msg" || params["user_id"] != "202" || !strings.Contains(text, want) {
						t.Fatalf("wrong reply to %s: %+v", command.text, action)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("private command timeout: %s", command.text)
				}
				if role == "admin" && command.text == "/ban 404" {
					target, _ := security.NewPrincipal("onebot", "404")
					if err := controller.CheckAccess(target); !errors.Is(err, security.ErrLocked) {
						t.Fatalf("private ban was not executed: %v", err)
					}
				}
			}
			target, _ := security.NewPrincipal("onebot", "404")
			if err := controller.CheckAccess(target); err != nil {
				t.Fatalf("target should be unlocked after commands: %v", err)
			}
			if role == "locked-admin" && !errors.Is(controller.CheckAccess(admin), security.ErrLocked) {
				t.Fatal("locked admin bypassed access check")
			}
		})
	}
}
