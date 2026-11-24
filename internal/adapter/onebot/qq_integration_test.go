package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestQQToolDeliveryAndRecallThroughAdapter(t *testing.T) {
	for _, tc := range []struct {
		tool       string
		rejectFile bool
	}{{qqextend.ToolName, false}, {qqextend.ToolName, true}, {"send_message", false}, {"send_message", true}} {
		t.Run(fmt.Sprintf("%s/reject=%v", tc.tool, tc.rejectFile), func(t *testing.T) {
			rejectFile := tc.rejectFile
			finished := make(chan struct{})
			providerCalls := 0
			provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				providerCalls++
				message := core.ChatMessage{Role: core.RoleAssistant}
				switch providerCalls {
				case 1:
					args := `{"action":"send","arguments":{"messages":[{"type":"plain","text":"prefix"},{"type":"file","url":"https://example.invalid/file","name":"fixture.txt"},{"type":"plain","text":"suffix"}]}}`
					if tc.tool == "send_message" {
						args = `{"messages":[{"type":"plain","text":"prefix"},{"type":"file","url":"https://example.invalid/file"},{"type":"plain","text":"suffix"}]}`
					}
					message.ToolCalls = []core.ToolCall{{ID: "send-fixture", Type: "function", Function: core.ToolCallFunction{Name: tc.tool, Arguments: args}}}
				case 2:
					result, _ := req.Messages[len(req.Messages)-1].Content.(string)
					items := qqItems(t, result)
					if len(items) != 3 || items[0].MessageID != "801" || items[0].Status != "confirmed" {
						t.Errorf("missing correlated result: %s", result)
					}
					if rejectFile {
						if items[1].Status != "rejected" || items[2].Status != "not_sent" {
							t.Errorf("failed suffix reported incorrectly: %s", result)
						}
						close(finished)
					} else {
						if items[2].Status != "confirmed" {
							t.Errorf("unconfirmed suffix: %s", result)
						}
						message.ToolCalls = []core.ToolCall{{ID: "recall-fixture", Type: "function", Function: core.ToolCallFunction{Name: "qq_extend_messages", Arguments: `{"action":"recall","arguments":{"message_id":"801"}}`}}}
					}
				case 3:
					result, _ := req.Messages[len(req.Messages)-1].Content.(string)
					if !strings.Contains(result, `"status":"confirmed"`) {
						t.Errorf("own ACK did not grant recall: %s", result)
					}
					close(finished)
				default:
					t.Errorf("unexpected provider round %d", providerCalls)
				}
				return &core.ChatResponse{Message: message}, nil
			}}
			engine := newTestEngine(provider)
			engine.ToolRegistry["qq_extend_messages"] = tools.QQTool()
			engine.ToolRegistry["send_message"] = tools.SendMsgTool()
			srv, address := startReplyWSTestServer(t, engine)
			defer srv.Close()
			ws, _, err := websocket.DefaultDialer.Dial(address, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			var writeMu sync.Mutex
			write := func(value any) error { writeMu.Lock(); defer writeMu.Unlock(); return ws.WriteJSON(value) }
			actions := make(chan string, 8)
			readError := make(chan error, 1)
			go func() {
				count := 0
				for {
					var action model.OneBotAction
					if err := ws.ReadJSON(&action); err != nil {
						readError <- err
						return
					}
					count++
					actions <- action.Action
					response := map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 800 + count}}
					if action.Action == "upload_private_file" && rejectFile {
						response["status"] = "failed"
						response["retcode"] = 100
						response["wording"] = "permission denied"
					}
					if err := write(response); err != nil {
						readError <- err
						return
					}
				}
			}()
			event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: testBotID, UserID: testUserID, MessageID: 101, Message: json.RawMessage(`[{"type":"text","data":{"text":"fixture request"}}]`)}
			if err := write(event); err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
			case err := <-readError:
				t.Fatal(err)
			case <-time.After(3 * time.Second):
				t.Fatal("QQ tool loop did not finish")
			}
			want := []string{"send_private_msg", "upload_private_file", "send_private_msg", "delete_msg"}
			if rejectFile {
				want = want[:2]
			}
			var got []string
			for range want {
				select {
				case action := <-actions:
					got = append(got, action)
				case <-time.After(time.Second):
					t.Fatal("missing action")
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("actions=%v want=%v", got, want)
			}
		})
	}
}

func TestQQFinalStructuredMediaUsesNativeUpload(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: `{"messages":[{"type":"file","url":"https://example.invalid/fixture","name":"shown.txt"}]}`}}}})
	srv, address := startReplyWSTestServer(t, engine)
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: testBotID, UserID: testUserID, MessageID: 101, Message: json.RawMessage(`[{"type":"text","data":{"text":"fixture request"}}]`)}
	if err := ws.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var action model.OneBotAction
	if err := ws.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	params := action.Params.(map[string]any)
	if action.Action != "upload_private_file" || params["name"] != "shown.txt" {
		t.Fatalf("final rich route skipped native upload: %+v", action)
	}
	if err := ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 801}}); err != nil {
		t.Fatal(err)
	}
}

func TestQQCrossSessionToolKeepsSourceAndTargetRepliesSeparate(t *testing.T) {
	provider := &mockLLMProvider{responses: []*core.ChatResponse{
		{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "external-fixture", Type: "function", Function: core.ToolCallFunction{Name: qqextend.ToolName, Arguments: `{"action":"send","arguments":{"target_group_id":"42","messages":[{"type":"plain","text":"external fixture"}]}}`}}}}},
		{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "source acknowledgement"}},
	}}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{qqextend.TargetGroupsKey: "42", "ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true"})
	defer engine.Scope.Cancel()
	engine.ToolRegistry[qqextend.ToolName] = tools.QQTool()
	server, address := startReplyWSTestServer(t, engine)
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	event := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: testBotID, UserID: testUserID, MessageID: 101, Message: json.RawMessage(`[{"type":"text","data":{"text":"relay fixture"}}]`)}
	if err := ws.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"send_group_msg", "send_private_msg"} {
		var action model.OneBotAction
		if err := ws.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		params := action.Params.(map[string]any)
		if action.Action != name {
			t.Fatalf("action=%+v want=%s", action, name)
		}
		if index == 0 && fmt.Sprint(params["group_id"]) != "42" || index == 1 && fmt.Sprint(params["user_id"]) != fmt.Sprint(testUserID) {
			t.Fatalf("incorrect destination: %+v", action)
		}
		response := qqOK(201 + index)
		response.Echo, _ = json.Marshal(action.Echo)
		if err := ws.WriteJSON(response); err != nil {
			t.Fatal(err)
		}
	}
	source := engine.SessionManager.GetOrCreate(fmt.Sprintf("private:%d", testUserID))
	target := engine.SessionManager.GetOrCreate("group:42")
	deadline := time.Now().Add(time.Second)
	for {
		sourceMessages := source.Messages()
		found := false
		for _, message := range sourceMessages {
			if message.Role == core.RoleAssistant && message.Content == "external fixture" {
				t.Fatal("target delivery polluted source history")
			}
			found = found || message.Role == core.RoleAssistant && message.Content == "source acknowledgement"
		}
		if found {
			messages := target.Messages()
			if len(messages) != 1 || messages[0].Content != "external fixture" {
				t.Fatalf("target history=%+v", messages)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source acknowledgement was not recorded")
		}
		time.Sleep(time.Millisecond)
	}
}
