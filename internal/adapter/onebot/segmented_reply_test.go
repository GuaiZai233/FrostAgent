package onebot

import (
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/gorilla/websocket"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func segmentTestEngine(t *testing.T, output string, enabled bool) *llm.Engine {
	t.Helper()
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{replyformat.EnvKey: fmt.Sprint(enabled), "STICKER_ACTIVITY": "0", "ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true"} {
		if err := config.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	engine := newTestEngine(&mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: output}}}})
	engine.Scope = runtimescope.New(config, nil, nil)
	return engine
}
func waitSegmentHistory(t *testing.T, engine *llm.Engine, key string, want []string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var got []string
		for _, msg := range engine.SessionManager.GetOrCreate(key).Snapshot() {
			if msg.Role == "assistant" {
				got = append(got, msg.Content.(string))
			}
		}
		if reflect.DeepEqual(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("history=%q want=%q", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestSegmentedReplyDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, output         string
		enabled, group, fail bool
		want                 []string
	}{
		{"private", "first" + replyformat.SegmentMark + "second\n\nthird", true, false, false, []string{"first", "second", "third"}},
		{"group", "first" + replyformat.SegmentMark + "second", true, true, false, []string{"first", "second"}},
		{"disabled", "first" + replyformat.SegmentMark + "second\n\n`" + replyformat.SegmentMark + "`", false, false, false, []string{"firstsecond\n\n`" + replyformat.SegmentMark + "`"}},
		{"partial failure", "first" + replyformat.SegmentMark + "second" + replyformat.SegmentMark + "third", true, false, true, []string{"first", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := segmentTestEngine(t, tc.output, tc.enabled)
			srv, url := startReplyWSTestServer(t, engine)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				srv.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); srv.Close() })
			// Deliberately tiny synthetic protocol IDs, never real account IDs.
			event := model.OneBotEvent{PostType: "message", SelfID: 7, UserID: 42, MessageID: 1, MessageType: "private", Message: json.RawMessage(`[{"type":"text","data":{"text":"hello"}}]`)}
			if tc.group {
				event.MessageType = "group"
				event.GroupID = 43
				event.Message = json.RawMessage(`[{"type":"at","data":{"qq":"7"}},{"type":"text","data":{"text":"hello"}}]`)
			}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			for index, want := range tc.want {
				var action model.OneBotAction
				for {
					if err := conn.ReadJSON(&action); err != nil {
						t.Fatal(err)
					}
					if action.Action != "get_group_info" {
						break
					}
					if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": 43, "group_name": "synthetic-group"}}); err != nil {
						t.Fatal(err)
					}
				}
				params := action.Params.(map[string]any)
				body := params["message"]
				if tc.group && index > 0 {
					// Follow-up segments are plain text, never a quote/mention chain.
					if _, plain := body.(string); !plain {
						t.Fatalf("follow-up segment %d is not plain text: %v", index, body)
					}
				}
				if tc.group && index == 0 {
					raw, _ := json.Marshal(body)
					var chain []struct {
						Type string
						Data map[string]any
					}
					if err := json.Unmarshal(raw, &chain); err != nil {
						t.Fatal(err)
					}
					var text strings.Builder
					quote, mention := false, false
					for _, part := range chain {
						switch part.Type {
						case "text":
							text.WriteString(part.Data["text"].(string))
						case "reply":
							quote = true
						case "at":
							mention = true
						}
					}
					if !quote || !mention {
						t.Fatalf("lost group decoration on first segment: %s", raw)
					}
					body = strings.TrimSpace(text.String())
				}
				if body != want {
					t.Fatalf("part %d=%v want=%q", index, body, want)
				}
				status, retcode := "ok", 0
				if tc.fail && index == 1 {
					status = "failed"
					retcode = 1
				}
				if err := conn.WriteJSON(map[string]any{"status": status, "retcode": retcode, "echo": action.Echo, "data": map[string]any{"message_id": 100 + index}}); err != nil {
					t.Fatal(err)
				}
			}
			key := "private:42"
			if tc.group {
				key = "group:43"
			}
			history := strings.Join(tc.want, "\n\n")
			if tc.fail {
				history = "first"
			}
			waitSegmentHistory(t, engine, key, []string{history})
			conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
			if _, _, err := conn.ReadMessage(); err == nil {
				t.Fatal("unexpected extra segment")
			}
		})
	}
}
func TestPartialReplyCannotResurrectResetSession(t *testing.T) {
	engine := segmentTestEngine(t, "", true)
	defer engine.Scope.Cancel()
	session := engine.SessionManager.GetOrCreate("synthetic-session")
	epoch := session.Epoch()
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	recordPartialReply(session, engine, epoch, true, "old delivered prefix")
	if len(session.Snapshot()) != 0 || len(session.SnapshotGroupContext(20, 4096, "").RecentStructuredMessages) != 0 {
		t.Fatal("old epoch restored after reset")
	}
}

func TestSegmentedReplyStopsAfterInterruption(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("reset=%v", reset), func(t *testing.T) {
			engine := segmentTestEngine(t, "first"+replyformat.SegmentMark+"second"+replyformat.SegmentMark+"third", true)
			server, url := startReplyWSTestServer(t, engine)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				server.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); server.Close() })
			event := model.OneBotEvent{PostType: "message", SelfID: 7, UserID: 42, MessageID: 1, MessageType: "private", Message: json.RawMessage(`"hello"`)}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			for i, want := range []string{"first", "second"} {
				var action model.OneBotAction
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Params.(map[string]any)["message"] != want {
					t.Fatalf("unexpected segment: %+v", action)
				}
				if i == 0 {
					if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 100}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			// The second send is pending without an ACK: only the first is known delivered.
			session := engine.SessionManager.GetOrCreate("private:42")
			barrier := session.ReserveSequenceTurn("sender:42", true, false)
			if reset {
				if err := session.ResetSession(nil); err != nil {
					t.Fatal(err)
				}
			}
			completed := make(chan []llm.ChatMessage, 1)
			go func() {
				barrier.Wait()
				var inherited []llm.ChatMessage
				if !reset {
					inherited, _, _ = session.PrepareTurnMessages(barrier, "successor-input", "successor-input", nil)
				}
				barrier.Done()
				completed <- inherited
			}()
			select {
			case inherited := <-completed:
				if !reset && (len(inherited) != 3 || inherited[0].Role != "user" || inherited[1].Role != "assistant" || inherited[1].Content != "first" || inherited[2].Content != "successor-input") {
					t.Fatalf("wrong successor transcript: %+v", inherited)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("interrupted turn did not finish")
			}
			if !reset {
				waitSegmentHistory(t, engine, "private:42", []string{"first"})
			}
			conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
			if _, _, err := conn.ReadMessage(); err == nil {
				t.Fatal("sent suffix after interruption")
			}
			if reset && len(session.Snapshot()) != 0 {
				t.Fatalf("reset history restored: %+v", session.Snapshot())
			}
		})
	}
}
