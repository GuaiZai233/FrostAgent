package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/envinject"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

// A real WebSocket peer observes the final destination and acknowledges writes.
func qqCrossSessionConnection(t *testing.T, respond func(model.OneBotAction) oneBotAPIResponse) *wsConnection {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			var action model.OneBotAction
			if ws.ReadJSON(&action) != nil {
				return
			}
			response := respond(action)
			response.Echo, _ = json.Marshal(action.Echo)
			if ws.WriteJSON(response) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newWSConnection(ws)
	t.Cleanup(func() { c.Close() })
	go func() {
		for {
			var response oneBotAPIResponse
			if ws.ReadJSON(&response) != nil {
				return
			}
			var echo string
			_ = json.Unmarshal(response.Echo, &echo)
			c.deliverMessageResponse(echo, response)
		}
	}()
	return c
}

func TestQQCrossSessionTargetsAndSourceTrust(t *testing.T) {
	for _, source := range []string{"group", "private"} {
		for _, actionName := range []string{"send", "forward_message", "forward_bundle"} {
			t.Run(source+"/"+actionName, func(t *testing.T) {
				actions := make(chan model.OneBotAction, 8)
				c := qqCrossSessionConnection(t, func(action model.OneBotAction) oneBotAPIResponse {
					actions <- action
					return qqOK(201)
				})
				c.Scope = newTestScopeWithEnv(t, map[string]string{qqextend.TargetGroupsKey: "42", "ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true"})
				defer c.Scope.Cancel()
				engine := newTestEngine(nil)
				engine.Scope = c.Scope
				event := model.OneBotEvent{SelfID: testBotID, UserID: testUserID, GroupID: testGroupID, MessageType: source, MessageID: 101}
				sourceSession := c.historyKey(event)
				c.rememberMessageSession(101, sourceSession)
				request := tools.QQRequest{Action: actionName, Arguments: tools.QQArguments{TargetGroupID: "42"}}
				switch actionName {
				case "send":
					request.Arguments.Messages = []tools.Msg{{Type: "plain", Text: "external fixture"}}
				case "forward_message":
					request.Arguments.MessageID = "101"
				case "forward_bundle":
					request.Arguments.Nodes = []tools.QQForwardNode{{MessageID: "101"}, {Messages: []tools.Msg{{Type: "plain", Text: "external fixture"}}}}
				}
				raw, _ := json.Marshal(request)
				sourceDeliveries := 0
				result, err := c.qqActionHook(event, nil, func([]tools.Msg) { sourceDeliveries++ }, engine)(context.Background(), string(raw))
				if err != nil || result.MessagesSent != 1 || result.ExternalMessagesSent != 1 || !strings.Contains(result.Content, `"target_session":"group:42"`) {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				select {
				case action := <-actions:
					params := action.Params.(map[string]any)
					if fmt.Sprint(params["group_id"]) != "42" || params["user_id"] != nil {
						t.Fatalf("wrong destination: %+v", action)
					}
					if actionName == "send" {
						wire, _ := json.Marshal(params["message"])
						var segments []tools.OneBotSegment
						_ = json.Unmarshal(wire, &segments)
						if len(segments) != 1 || segments[0].Type != "text" {
							t.Fatalf("source decoration leaked: %s", wire)
						}
					}
				case <-time.After(time.Second):
					t.Fatal("no target action")
				}
				if sourceDeliveries != 0 || c.ownMessageSessionMatches(201, sourceSession) || !c.ownMessageSessionMatches(201, "group:42") {
					t.Fatal("delivery ownership leaked into source")
				}
				target := engine.SessionManager.GetOrCreate("group:42")
				target.Lock()
				count := len(target.History)
				target.Unlock()
				if count != 1 {
					t.Fatalf("target history count=%d", count)
				}
				// A message observed only in the destination is not a trusted source.
				request.Action, request.Arguments = "forward_message", tools.QQArguments{TargetGroupID: "42", MessageID: "201"}
				raw, _ = json.Marshal(request)
				if _, err := c.qqActionHook(event, nil, nil, engine)(context.Background(), string(raw)); err == nil {
					t.Fatal("destination message accepted as source")
				}
				select {
				case action := <-actions:
					t.Fatalf("untrusted source sent: %+v", action)
				default:
				}
			})
		}
	}
}

func TestQQCrossSessionRejectsTargetsAndQuotesBeforeWriting(t *testing.T) {
	for _, allowed := range []string{"", "43", "42,bad", "42"} {
		t.Run(allowed, func(t *testing.T) {
			c := newWSConnection(nil)
			defer c.Close()
			c.Scope = newTestScopeWithEnv(t, map[string]string{qqextend.TargetGroupsKey: allowed})
			defer c.Scope.Cancel()
			event := model.OneBotEvent{SelfID: testBotID, UserID: testUserID, MessageType: "private", MessageID: 101}
			c.rememberMessageSession(101, c.historyKey(event))
			messages := []tools.Msg{{Type: "plain", Text: "must not send"}}
			if allowed == "42" {
				messages = append(messages, tools.Msg{Type: "quote", MessageID: "101"})
			}
			raw, _ := json.Marshal(tools.QQRequest{Action: "send", Arguments: tools.QQArguments{TargetGroupID: "42", Messages: messages}})
			_, err := c.qqActionHook(event, nil, nil, newTestEngine(nil))(context.Background(), string(raw))
			if err == nil || strings.Contains(err.Error(), "websocket") {
				t.Fatalf("failed after preflight: %v", err)
			}
		})
	}
}

func TestQQCrossSessionRevocationStopsSplitSends(t *testing.T) {
	for _, revoke := range []string{"whitelist", "target reset"} {
		t.Run(revoke, func(t *testing.T) {
			scope := newTestScopeWithEnv(t, map[string]string{qqextend.TargetGroupsKey: "42"})
			defer scope.Cancel()
			engine := newTestEngine(nil)
			engine.Scope = scope
			actions := make(chan string, 8)
			c := qqCrossSessionConnection(t, func(action model.OneBotAction) oneBotAPIResponse {
				actions <- action.Action
				if revoke == "whitelist" {
					if err := scope.Config.Update(qqextend.TargetGroupsKey, "", false); err != nil {
						t.Error(err)
					}
				} else {
					if err := engine.SessionManager.ResetSession("group:42"); err != nil {
						t.Error(err)
					}
				}
				return qqOK(201)
			})
			c.Scope = scope
			event := model.OneBotEvent{SelfID: testBotID, UserID: testUserID, MessageType: "private", MessageID: 101}
			request := tools.QQRequest{Action: "send", Arguments: tools.QQArguments{TargetGroupID: "42", Messages: []tools.Msg{{Type: "plain", Text: "first"}, {Type: "file", URL: "https://example.invalid/file"}, {Type: "plain", Text: "last"}}}}
			raw, _ := json.Marshal(request)
			result, err := c.qqActionHook(event, nil, nil, engine)(context.Background(), string(raw))
			if err != nil {
				t.Fatal(err)
			}
			items := qqItems(t, result.Content)
			if items[0].Status != "confirmed" || items[1].Status != "rejected" || items[2].Status != "not_sent" {
				t.Fatal(result.Content)
			}
			if got := <-actions; got != "send_group_msg" {
				t.Fatal(got)
			}
			select {
			case action := <-actions:
				t.Fatalf("sent after revocation: %s", action)
			default:
			}
		})
	}
}

func TestQQCrossSessionSQLWhitelistIgnoresInjectedEnvironment(t *testing.T) {
	previous := envinject.Suspend()
	t.Cleanup(func() {
		envinject.Suspend()
		if err := envinject.Restore(previous); err != nil {
			t.Error(err)
		}
	})
	t.Setenv(qqextend.TargetGroupsKey, "42")
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := envinject.Put(context.Background(), db, qqextend.TargetGroupsKey, "42"); err != nil {
		t.Fatal(err)
	}
	if err := envinject.ApplyAtStartup(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	config, err := instanceconfig.OpenDatabase(db, "synthetic-qq-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	if scope.Getenv(qqextend.TargetGroupsKey) != "42" {
		t.Fatal("fixture did not exercise the injected fallback")
	}
	actions := make(chan string, 8)
	c := qqCrossSessionConnection(t, func(action model.OneBotAction) oneBotAPIResponse { actions <- action.Action; return qqOK(201) })
	c.Scope = scope
	engine := newTestEngine(nil)
	engine.Scope = scope
	event := model.OneBotEvent{SelfID: testBotID, UserID: testUserID, MessageType: "private", MessageID: 101}
	request := `{"action":"send","arguments":{"target_group_id":"42","messages":[{"type":"plain","text":"fixture"}]}}`
	for _, step := range []struct {
		setting string
		allowed bool
	}{{"", false}, {"42", true}, {"", false}} {
		if err := config.Update(qqextend.TargetGroupsKey, step.setting, false); err != nil {
			t.Fatal(err)
		}
		discovery, err := tools.QQTool().ExecuteResult(runtimescope.WithContext(context.Background(), scope), `{"action":"discover"}`)
		if err != nil {
			t.Fatal(err)
		}
		wantGroups := `"target_groups":[]`
		if step.allowed {
			wantGroups = `"target_groups":["42"]`
		}
		if !strings.Contains(discovery.Content, wantGroups) {
			t.Fatal(discovery.Content)
		}
		result, err := c.qqActionHook(event, nil, nil, engine)(context.Background(), request)
		if step.allowed {
			if err != nil || result.MessagesSent != 1 {
				t.Fatalf("%+v %v", result, err)
			}
			if got := <-actions; got != "send_group_msg" {
				t.Fatal(got)
			}
		} else if err == nil {
			t.Fatal("injected target authorized an empty instance whitelist")
		}
		select {
		case action := <-actions:
			t.Fatalf("unexpected send: %s", action)
		default:
		}
	}
	other, err := instanceconfig.OpenDatabase(db, "synthetic-other-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	otherScope := runtimescope.New(other, nil, nil)
	defer otherScope.Cancel()
	if qqextend.ConfiguredTargets(otherScope) != "" {
		t.Fatal("target whitelist leaked into another instance")
	}
}
