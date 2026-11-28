package onebot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/gorilla/websocket"
)

// Numeric identities are small synthetic transport fixtures.
const (
	tempChatSelfID  = 101
	tempChatUserID  = 202
	tempChatGroupID = 303
)

func tempChatEvent(messageType string, message string) model.OneBotEvent {
	event := model.OneBotEvent{SelfID: tempChatSelfID, UserID: tempChatUserID, MessageID: 5,
		PostType: "message", MessageType: messageType, Message: json.RawMessage(message)}
	if messageType == "group" {
		event.GroupID = tempChatGroupID
	}
	return event
}

func TestParseTempChatCommand(t *testing.T) {
	text := func(s string) string { return fmt.Sprintf(`[{"type":"text","data":{"text":%q}}]`, s) }
	for _, tc := range []struct {
		name       string
		message    string
		noPersona  bool
		noMemory   bool
		wantPrompt string // text of the rebuilt message, "" when none
		empty      bool
		match      bool
	}{
		{name: "plain", message: text("/tempchat hello"), wantPrompt: "hello", match: true},
		{name: "np", message: text("/tempchat -np hello"), noPersona: true, wantPrompt: "hello", match: true},
		{name: "nm", message: text("/tempchat -nm hello"), noMemory: true, wantPrompt: "hello", match: true},
		{name: "np then nm", message: text("/tempchat -np -nm hello"), noPersona: true, noMemory: true, wantPrompt: "hello", match: true},
		{name: "nm then np", message: text("/tempchat -nm -np hello"), noPersona: true, noMemory: true, wantPrompt: "hello", match: true},
		{name: "flag case", message: text("/tempchat -NP -Nm hello"), noPersona: true, noMemory: true, wantPrompt: "hello", match: true},
		{name: "repeated flag", message: text("/tempchat -np -np hello"), noPersona: true, wantPrompt: "hello", match: true},
		{name: "newline after flags", message: text("/tempchat -np\nline one\nline two"), noPersona: true, wantPrompt: "line one\nline two", match: true},
		{name: "prompt keeps inner spacing", message: text("  /tempchat   -nm  a  b "), noMemory: true, wantPrompt: "a  b ", match: true},
		{name: "flag after prompt is prompt", message: text("/tempchat hello -np"), wantPrompt: "hello -np", match: true},
		{name: "flag lookalike is prompt", message: text("/tempchat -npx hello"), wantPrompt: "-npx hello", match: true},
		{name: "slash prompt stays prompt", message: text("/tempchat /reset"), wantPrompt: "/reset", match: true},
		{name: "reply and self at", message: `[{"type":"reply","data":{"id":"77"}},{"type":"at","data":{"qq":"101"}},{"type":"text","data":{"text":" /tempchat -np hi"}}]`, noPersona: true, wantPrompt: "hi", match: true},
		{name: "bare command", message: text("/tempchat"), empty: true, match: true},
		{name: "flags only", message: text("/tempchat -np -nm"), noPersona: true, noMemory: true, empty: true, match: true},
		{name: "quote and flags only", message: `[{"type":"reply","data":{"id":"77"}},{"type":"text","data":{"text":"/tempchat -nm"}}]`, noMemory: true, empty: true, match: true},
		{name: "image after command", message: `[{"type":"text","data":{"text":"/tempchat"}},{"type":"image","data":{"file":"synthetic"}}]`, match: true},
		{name: "mid sentence", message: text("please /tempchat hello")},
		{name: "longer word", message: text("/tempchats hello")},
		{name: "uppercase command", message: text("/TEMPCHAT hello")},
		{name: "other at", message: `[{"type":"at","data":{"qq":"404"}},{"type":"text","data":{"text":"/tempchat hi"}}]`},
		{name: "image before", message: `[{"type":"image","data":{"file":"synthetic"}},{"type":"text","data":{"text":"/tempchat hi"}}]`},
		{name: "other command", message: text("/msgid")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, ok := parseTempChatCommand(tempChatEvent("private", tc.message))
			if ok != tc.match {
				t.Fatalf("match=%v, want %v", ok, tc.match)
			}
			if !ok {
				return
			}
			if request.options.NoPersonality != tc.noPersona || request.options.NoMemory != tc.noMemory {
				t.Fatalf("options %+v, want np=%v nm=%v", request.options, tc.noPersona, tc.noMemory)
			}
			if request.empty != tc.empty {
				t.Fatalf("empty=%v, want %v", request.empty, tc.empty)
			}
			var prompt strings.Builder
			for _, segment := range ParseMessageSegments(request.event.Message) {
				if segment.Type == "text" {
					prompt.WriteString(segment.Data["text"].(string))
				}
			}
			if prompt.String() != tc.wantPrompt {
				t.Fatalf("prompt %q, want %q", prompt.String(), tc.wantPrompt)
			}
			if strings.Contains(string(request.event.Message), "tempchat") && tc.name != "slash prompt stays prompt" {
				t.Fatalf("command text leaked into the rebuilt message: %s", request.event.Message)
			}
		})
	}

	t.Run("non message events", func(t *testing.T) {
		notice := tempChatEvent("private", `"/tempchat hi"`)
		notice.PostType = "notice"
		poke := tempChatEvent("private", `"/tempchat hi"`)
		poke.PokeID = "synthetic"
		other := tempChatEvent("guild", `"/tempchat hi"`)
		for _, event := range []model.OneBotEvent{notice, poke, other} {
			if _, ok := parseTempChatCommand(event); ok {
				t.Fatalf("accepted %+v", event)
			}
		}
	})

	t.Run("group without at", func(t *testing.T) {
		if _, ok := parseTempChatCommand(tempChatEvent("group", `"/tempchat hi"`)); !ok {
			t.Fatal("group command must not require an @")
		}
	})
}

func TestTempChatQuoteGuidanceAndResponseContext(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	defer engine.Cancel()
	store := engine.Log()
	store.RecordMessageRef(logs.MessageRef{Platform: "onebot", SessionID: "group:303", MessageID: "600", Action: "send_group_msg",
		TempChat: &logs.TempChatMark{NoPersonality: true, NoMemory: true}})
	store.RecordMessageRef(logs.MessageRef{Platform: "onebot", SessionID: "group:303", MessageID: "601", Action: "send_group_msg"})
	conn := &wsConnection{engine: engine}
	event := tempChatEvent("group", `"hi"`)

	want := llm.TempChatQuotedGuidance(logs.TempChatMark{NoPersonality: true, NoMemory: true})
	if got := conn.tempChatQuoteGuidance(event, resolvedReplyContext{MessageID: "600", SessionID: "group:303"}); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	for name, quoted := range map[string]resolvedReplyContext{
		"ordinary bot message": {MessageID: "601", SessionID: "group:303"},
		"unknown message":      {MessageID: "602", SessionID: "group:303"},
		"no quote":             {},
		"other session":        {MessageID: "600", SessionID: "group:999"},
	} {
		if got := conn.tempChatQuoteGuidance(event, quoted); got != "" {
			t.Errorf("%s: guidance %q", name, got)
		}
	}
	if got := (&wsConnection{engine: engine, mock: true}).tempChatQuoteGuidance(event, resolvedReplyContext{MessageID: "600", SessionID: "group:303"}); got != "" {
		t.Errorf("mock connection: guidance %q", got)
	}

	// The note closes the response_context guidance, after the time line.
	var context struct {
		Guidance string `json:"guidance"`
	}
	if err := json.Unmarshal([]byte(buildResponseContext(event, GroupWakeSignals{AtBot: true}, false, want)), &context); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(context.Guidance, "\n"+want) {
		t.Fatalf("guidance does not end with the note: %q", context.Guidance)
	}
	if err := json.Unmarshal([]byte(buildResponseContext(event, GroupWakeSignals{AtBot: true}, false, "")), &context); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(context.Guidance, "临时对话") {
		t.Fatalf("note added without a temporary message: %q", context.Guidance)
	}
}

func TestRecordBotMessageRefCarriesTempChat(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	defer engine.Cancel()
	conn := &wsConnection{engine: engine}
	action := model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 202, "message": "synthetic"},
		TempChat: &model.TempChatOrigin{NoMemory: true}}
	conn.recordBotMessageRef("private:202", action, oneBotAPIResponse{Data: json.RawMessage(`{"message_id":700}`)}, false)

	mark, ok := engine.Log().FindTempChatMark("private:202", "700")
	if !ok || mark.NoPersonality || !mark.NoMemory {
		t.Fatalf("got %+v, %v", mark, ok)
	}
	if _, ok := engine.Log().FindMessageRefByMessageID("private:202", "700"); !ok {
		t.Fatal("a temporary message must still get a RefID")
	}
	if encoded, _ := json.Marshal(action); strings.Contains(string(encoded), "TempChat") || strings.Contains(string(encoded), "no_memory") {
		t.Fatalf("local metadata reached the platform: %s", encoded)
	}
}

func TestWSTempChatLeavesNoTrace(t *testing.T) {
	t.Setenv("SYSTEM_PROMPT", "SYNTH-SYSTEM-PROMPT")
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Security = security.NewController(t.TempDir())
	defer engine.Cancel()
	private := engine.SessionManager.GetOrCreate("private:202")
	private.AddMessageAtEpoch(core.ChatMessage{Role: core.RoleUser, Content: "SYNTH-EARLIER"}, 0)

	srv, wsURL := startReplyWSTestServer(t, engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	nextMessageID := int32(9000)
	// send delivers one event, acknowledges the bot's reply and returns the
	// platform message ID the bot's message received.
	send := func(event model.OneBotEvent) (model.OneBotAction, string) {
		t.Helper()
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var action model.OneBotAction
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		nextMessageID++
		if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo,
			"data": map[string]any{"message_id": nextMessageID}}); err != nil {
			t.Fatal(err)
		}
		return action, fmt.Sprint(nextMessageID)
	}
	lastRequest := func(t *testing.T, wantCount int) core.ChatRequest {
		t.Helper()
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if len(provider.requests) != wantCount {
			t.Fatalf("provider saw %d requests, want %d", len(provider.requests), wantCount)
		}
		return provider.requests[wantCount-1]
	}
	flatten := func(req core.ChatRequest) string {
		encoded, _ := json.Marshal(req.Messages)
		return string(encoded)
	}
	private202 := func(id int32, text string) model.OneBotEvent {
		event := tempChatEvent("private", fmt.Sprintf(`[{"type":"text","data":{"text":%q}}]`, text))
		event.MessageID = id
		return event
	}

	// -np -nm: no persona, no history, and the command text is gone.
	_, firstID := send(private202(11, "/tempchat -np -nm 你好"))
	request := lastRequest(t, 1)
	text := flatten(request)
	if strings.Contains(text, "SYNTH-SYSTEM-PROMPT") || strings.Contains(text, "SYNTH-EARLIER") {
		t.Fatalf("-np -nm run saw persona or history: %s", text)
	}
	if !strings.Contains(text, "User Message: 你好") || strings.Contains(text, "/tempchat") || strings.Contains(text, "-np") {
		t.Fatalf("prompt not isolated from the command: %s", text)
	}

	// Bare /tempchat keeps persona and history readable.
	_, secondID := send(private202(12, "/tempchat 还记得吗"))
	text = flatten(lastRequest(t, 2))
	if !strings.Contains(text, "SYNTH-SYSTEM-PROMPT") || !strings.Contains(text, "SYNTH-EARLIER") {
		t.Fatalf("bare /tempchat lost persona or history: %s", text)
	}

	// Another reserved command in the prompt is only text.
	send(private202(13, "/tempchat /msgid"))
	if text = flatten(lastRequest(t, 3)); !strings.Contains(text, "User Message: /msgid") {
		t.Fatalf("slash prompt did not reach the model: %s", text)
	}

	// Every reply was marked, with the options of its own run.
	waitMark := func(messageID string) logs.TempChatMark {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if mark, ok := engine.Log().FindTempChatMark("private:202", messageID); ok {
				return mark
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("reply %s was not marked as temporary", messageID)
		return logs.TempChatMark{}
	}
	if mark := waitMark(firstID); !mark.NoPersonality || !mark.NoMemory {
		t.Fatalf("first mark %+v", mark)
	}
	if mark := waitMark(secondID); mark.NoPersonality || mark.NoMemory {
		t.Fatalf("second mark %+v", mark)
	}
	if _, ok := engine.Log().FindMessageRefByMessageID("private:202", firstID); !ok {
		t.Fatal("temporary reply has no RefID")
	}

	// Nothing reached the session, even after commit time.
	time.Sleep(200 * time.Millisecond)
	history := private.Snapshot()
	if len(history) != 1 || history[0].Content != "SYNTH-EARLIER" {
		t.Fatalf("temporary conversation changed history: %+v", history)
	}

	// An ordinary turn afterwards sees none of the temporary exchange.
	_, plainID := send(private202(14, "普通消息"))
	text = flatten(lastRequest(t, 4))
	if !strings.Contains(text, "SYNTH-SYSTEM-PROMPT") || !strings.Contains(text, "SYNTH-EARLIER") || !strings.Contains(text, "普通消息") {
		t.Fatalf("ordinary turn broke: %s", text)
	}
	for _, leaked := range []string{"你好", "还记得吗", "/msgid"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("ordinary turn saw temporary content %q: %s", leaked, text)
		}
	}
	if _, ok := engine.Log().FindTempChatMark("private:202", plainID); ok {
		t.Fatal("ordinary reply was marked as temporary")
	}

	// An empty command explains itself without calling the model.
	if err := conn.WriteJSON(private202(15, "/tempchat -np")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var usage model.OneBotAction
	if err := conn.ReadJSON(&usage); err != nil {
		t.Fatal(err)
	}
	params, _ := usage.Params.(map[string]any)
	segments, _ := params["message"].([]any)
	if usage.Action != "send_private_msg" || len(segments) != 1 ||
		segments[0].(map[string]any)["data"].(map[string]any)["text"] != tempChatUsage {
		t.Fatalf("unexpected usage reply: %+v", usage)
	}
	lastRequest(t, 4)
}

func TestWSTempChatInGroupNeedsNoMentionAndIsNeverBuffered(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Security = security.NewController(t.TempDir())
	defer engine.Cancel()

	srv, wsURL := startReplyWSTestServer(t, engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	event := tempChatEvent("group", `[{"type":"text","data":{"text":"/tempchat -nm 群里问一下"}}]`)
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var action model.OneBotAction
	for {
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if action.Action == "send_group_msg" {
			break
		}
		// Platform lookups such as the group name are answered by the fake platform.
		if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo,
			"data": map[string]any{"group_id": tempChatGroupID, "group_name": "synthetic group"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo,
		"data": map[string]any{"message_id": 9100}}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := engine.Log().FindTempChatMark("group:303", "9100"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("group reply was not marked as temporary")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	provider.mu.Lock()
	requests := len(provider.requests)
	provider.mu.Unlock()
	if requests != 1 {
		t.Fatalf("provider saw %d requests, want 1", requests)
	}
	session := engine.SessionManager.GetOrCreate("group:303")
	if history := session.Snapshot(); len(history) != 0 {
		t.Fatalf("group history changed: %+v", history)
	}
	if snapshot := session.SnapshotGroupContext(20, 10000, ""); len(snapshot.RecentStructuredMessages) != 0 {
		t.Fatalf("temporary messages were buffered for group compaction: %+v", snapshot.RecentStructuredMessages)
	}
}
