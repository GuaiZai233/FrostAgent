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

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/gorilla/websocket"
)

func enableHistoryTest(t *testing.T, engine *llm.Engine) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInstance(context.Background(), storage.InstanceRecord{ID: "fixture-instance", Name: "Fixture", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	engine.ChatHistory = chathistory.NewRecorder(chathistory.NewStore(db, "fixture-instance"), engine.Getenv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); engine.ChatHistory.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; db.Close() })
	return db
}

func TestOneBotHistoryBeforeWakeAndWhitelist(t *testing.T) {
	const fixtureGroup int64 = 42
	engine := newTestEngine(&mockLLMProvider{})
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		chathistory.GroupWhitelistKey: strconv.FormatInt(fixtureGroup, 10), "GROUP_REPLY_ON_MENTION": "false",
	})
	enableHistoryTest(t, engine)
	adapter := NewAdapter(engine)
	srv := httptest.NewServer(http.HandlerFunc(adapter.Handler()))
	defer srv.Close()
	defer adapter.CloseConnections()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	event := model.OneBotEvent{PostType: "message", MessageType: "group", GroupID: fixtureGroup, UserID: 43, SelfID: 44, MessageID: 1, Time: 123,
		Message:  json.RawMessage(`[{"type":"text","data":{"text":"未经唤醒原文"}},{"type":"image","data":{"url":"https://media.invalid/private"}}]`),
		Messages: json.RawMessage(`["packed history must not be archived"]`),
	}
	if err := client.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		records, err := engine.ChatHistory.Store.Export(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(records) > 0 {
			if len(records) != 1 || records[0].Content != "未经唤醒原文[image]" || records[0].OccurredAt.Unix() != 123 {
				t.Fatalf("wrong originals: %#v", records)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unawakened message was not persisted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Check collection policy at the same ingress hook, including mock exclusion.
	c := &wsConnection{engine: engine}
	event.GroupID = 45
	event.MessageID = 2
	c.recordIncomingHistory(event)
	event.GroupID = fixtureGroup
	c.mock = true
	c.recordIncomingHistory(event)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.ChatHistory.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	records, _ := engine.ChatHistory.Store.Export(ctx)
	if len(records) != 1 {
		t.Fatalf("nonwhitelisted or mock collection: %#v", records)
	}
}

func TestOneBotHistoryACKAndFailure(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			engine := newTestEngine(&mockLLMProvider{})
			enableHistoryTest(t, engine)
			result := make(chan error, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					result <- err
					return
				}
				c := newWSConnection(wire)
				c.engine = engine
				defer c.Close()
				readDone := make(chan struct{})
				go func() {
					defer close(readDone)
					_, data, err := wire.ReadMessage()
					if err == nil {
						c.handleAPIResponse(data)
					}
				}()
				_, err = c.SendActionAndWait(model.OneBotAction{Action: "send_private_msg", Params: map[string]any{"user_id": 42, "message": "sent original[CQ:image,file=https://media.invalid/ack-image]"}}, time.Second)
				<-readDone
				result <- err
			}))
			defer srv.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var action model.OneBotAction
			if err := client.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			status, code := "failed", 1400
			if accepted {
				status, code = "ok", 0
			}
			if err := client.WriteJSON(map[string]any{"status": status, "retcode": code, "echo": action.Echo, "data": map[string]any{"message_id": 7}}); err != nil {
				t.Fatal(err)
			}
			err = <-result
			if accepted != (err == nil) {
				t.Fatalf("ACK result %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := engine.ChatHistory.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			records, err := engine.ChatHistory.Store.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !accepted && len(records) != 0 {
				t.Fatal("failed reply archived")
			}
			if accepted && (len(records) != 1 || records[0].DeliveryStatus != "delivered" || records[0].MessageID != "7" || records[0].Content != "sent original[image]") {
				t.Fatalf("ACK archive: %#v", records)
			}
		})
	}
}
