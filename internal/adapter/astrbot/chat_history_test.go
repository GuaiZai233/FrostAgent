package astrbot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/gorilla/websocket"
)

func enableHistoryTest(t *testing.T, engine *llm.Engine) {
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
}

func TestAstrBotHistoryBeforeWakeAndPackedContext(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	engine.Scope = newTestScopeWithEnv(t, map[string]string{chathistory.GroupWhitelistKey: "telegram:group:fixture-group", "GROUP_REPLY_ON_MENTION": "false"})
	enableHistoryTest(t, engine)
	srv, adapter, url := startWSTestServer(engine)
	defer srv.Close()
	defer adapter.CloseConnections()
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	event := Event{Type: "event", EventType: "message", Platform: "telegram", MessageType: "group", GroupID: "fixture-group", UserID: "fixture-user", MessageID: "fixture-current",
		Content: "当前原文", Messages: []string{"packed previous history"}, Timestamp: 123,
		Attachments: []core.Attachment{{Type: core.AttachmentTypeImage, MessageID: "fixture-current", URL: "https://media.invalid/private"}, {Type: core.AttachmentTypeImage, MessageID: "fixture-old"}}}
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
			if len(records) != 1 || records[0].Content != "当前原文[image]" || records[0].OccurredAt.Unix() != 123 {
				t.Fatalf("wrong originals: %#v", records)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unawakened message was not persisted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	c := &wsConn{engine: engine, mock: true}
	event.MessageID = "fixture-mock"
	c.recordIncomingHistory(event)
	c.mock = false
	event.Platform = "qq"
	c.recordIncomingHistory(event)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.ChatHistory.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	records, _ := engine.ChatHistory.Store.Export(ctx)
	if len(records) != 1 {
		t.Fatalf("mock or cross platform collection: %#v", records)
	}
}

func TestAstrBotHistoryDeliveryReceipt(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			engine := newTestEngine(&mockLLMProvider{})
			enableHistoryTest(t, engine)
			server, client := newChatHistoryWire(t)
			c := newWSConn(server)
			c.engine = engine
			defer c.Close()
			defer client.Close()
			done := make(chan error, 1)
			go func() {
				done <- c.writeActionAndWaitTurn(Action{Action: "send_message", Platform: "telegram", MessageType: "private", TargetID: "fixture-user", Content: "reply original"}, nil, time.Second, "test")
			}()
			var action Action
			if err := client.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			c.acceptDeliveryReceipt(deliveryReceipt{DeliveryID: action.DeliveryID, Delivered: accepted})
			err := <-done
			if accepted != (err == nil) {
				t.Fatalf("receipt result %v", err)
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
			if accepted && (len(records) != 1 || records[0].DeliveryStatus != "delivered" || records[0].Content != "reply original") {
				t.Fatalf("receipt archive: %#v", records)
			}
		})
	}
}

func newChatHistoryWire(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	serverConn := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConn <- c
	}))
	t.Cleanup(srv.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return <-serverConn, client
}
