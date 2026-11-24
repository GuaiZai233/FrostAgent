package onebot

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"testing"

	"github.com/gorilla/websocket"
)

func TestOneBotSilentIgnoreDropsUserBeforeAnyProcessing(t *testing.T) {
	provider := botLoopSilentProvider()
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{"SILENT_IGNORE_USER_IDS": "11"})
	engine.ToolRegistry[llm.StaySilentToolName] = tools.StaySilentTool()
	srv, wsURL := startReplyWSTestServer(t, engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	message, _ := json.Marshal("hello")
	// Events are handled in arrival order, so once the second user's request
	// has reached the model, the ignored user's event was already consumed.
	for id, user := range []int64{11, 12} {
		event := model.OneBotEvent{PostType: "message", MessageType: "private", UserID: user, MessageID: int32(id + 1), Message: message}
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
	}
	waitOneBotRequests(t, provider, 1)

	provider.mu.Lock()
	requests := len(provider.requests)
	provider.mu.Unlock()
	if requests != 1 {
		t.Fatalf("only the non-ignored user may reach the model, got %d requests", requests)
	}
	if history := engine.SessionManager.GetOrCreate("private:11").Snapshot(); len(history) != 0 {
		t.Fatalf("ignored user must leave no session history, got %d messages", len(history))
	}
	if history := engine.SessionManager.GetOrCreate("private:12").Snapshot(); len(history) == 0 {
		t.Fatal("control user must be processed normally")
	}
}
