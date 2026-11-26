package onebot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

func TestOneBotConfirmedReplyReasoningNextTurn(t *testing.T) {
	for _, tc := range []struct {
		name, output, history, reasoning string
		parts                            []any
		failLast                         bool
	}{
		{name: "unchanged", output: "first-answer", history: "first-answer", reasoning: "synthetic-reasoning", parts: []any{"first-answer"}},
		{name: "segmented", output: "first" + replyformat.SegmentMark + "second", history: "first\n\nsecond", parts: []any{"first", "second"}},
		{name: "partial", output: "first" + replyformat.SegmentMark + "unsent", history: "first", parts: []any{"first", "unsent"}, failLast: true},
		{
			name: "structured JSON", output: `{"messages":[{"type":"plain","text":"hi"}]}`,
			history: `{"messages":[{"type":"plain","text":"hi"}]}`,
			parts:   []any{[]tools.OneBotSegment{{Type: "text", Data: map[string]any{"text": "hi"}}}},
		},
		{
			name: "native media", output: `{"messages":[{"type":"image","url":"https://example.invalid/synthetic.png"}]}`,
			history: `{"messages":[{"type":"image","url":"https://example.invalid/synthetic.png"}]}`,
			parts:   []any{[]tools.OneBotSegment{{Type: "image", Data: map[string]any{"file": "https://example.invalid/synthetic.png"}}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				if call == 1 {
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": tc.output, "reasoning_content": "synthetic-reasoning"}}}})
					return
				}
				var body struct {
					Messages []struct {
						Role      string  `json:"role"`
						Content   any     `json:"content"`
						Reasoning *string `json:"reasoning_content"`
					} `json:"messages"`
					Tools []json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if call != 2 || len(body.Tools) == 0 {
					t.Errorf("unexpected request: call=%d tools=%d", call, len(body.Tools))
				}
				assistants := 0
				for _, message := range body.Messages {
					if message.Role != "assistant" {
						continue
					}
					assistants++
					if message.Content != tc.history || message.Reasoning == nil || *message.Reasoning != tc.reasoning {
						t.Errorf("confirmed history lost or misbound reasoning: content=%v reasoning=%v want content=%q reasoning=%q", message.Content, message.Reasoning, tc.history, tc.reasoning)
						http.Error(w, "missing reasoning_content", http.StatusBadRequest)
						return
					}
				}
				if assistants != 1 {
					t.Errorf("assistant history count=%d", assistants)
				}
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"next-answer","reasoning_content":"next-reasoning"}}]}`)
			}))
			defer upstream.Close()
			engine := segmentTestEngine(t, "unused", true)
			engine.Provider = api.NewClient(upstream.URL, "", api.ChatCompletions, 0)
			engine.ToolRegistry["stay_silent"] = tools.StaySilentTool()
			server, url := startReplyWSTestServer(t, engine)
			socket, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				server.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { socket.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); server.Close() })
			// Tiny synthetic protocol IDs only.
			event := model.OneBotEvent{PostType: "message", SelfID: 7, UserID: 42, MessageID: 1, MessageType: "private", Message: json.RawMessage(`"first-input"`)}
			if err := socket.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			socket.SetReadDeadline(time.Now().Add(5 * time.Second))
			acknowledge := func(want any, fail bool) {
				t.Helper()
				var action model.OneBotAction
				if err := socket.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				wantJSON, _ := json.Marshal(want)
				var wantMessage any
				if err := json.Unmarshal(wantJSON, &wantMessage); err != nil {
					t.Fatal(err)
				}
				if action.Action != "send_private_msg" || !reflect.DeepEqual(action.Params.(map[string]any)["message"], wantMessage) {
					t.Fatalf("unexpected action: %+v, want=%s", action, wantJSON)
				}
				status, code := "ok", 0
				if fail {
					status, code = "failed", 1
				}
				if err := socket.WriteJSON(map[string]any{"status": status, "retcode": code, "echo": action.Echo, "data": map[string]any{"message_id": 100}}); err != nil {
					t.Fatal(err)
				}
			}
			for i, part := range tc.parts {
				acknowledge(part, tc.failLast && i == len(tc.parts)-1)
			}
			waitSegmentHistory(t, engine, "private:42", []string{tc.history})
			event.MessageID++
			event.Message = json.RawMessage(`"second-input"`)
			if err := socket.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			acknowledge("next-answer", false)
			waitSegmentHistory(t, engine, "private:42", []string{tc.history, "next-answer"})
			history := engine.SessionManager.GetOrCreate("private:42").Snapshot()
			for _, message := range history {
				if message.Role == "assistant" && message.Content == tc.history {
					if (message.ProviderState != nil) != (tc.reasoning != "") {
						t.Errorf("unexpected persisted state for %s", tc.name)
					}
				}
				if message.Role == "assistant" && tc.failLast && strings.Contains(fmt.Sprint(message.Content), "unsent") {
					t.Fatal("unsent suffix entered history")
				}
			}
			if calls.Load() != 2 {
				t.Errorf("upstream calls=%d", calls.Load())
			}
		})
	}
}
