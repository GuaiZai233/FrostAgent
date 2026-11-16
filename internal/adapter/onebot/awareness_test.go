package onebot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/gorilla/websocket"
)

func TestAwarenessOneBotNativeMentionAndACK(t *testing.T) {
	adapter := NewAdapter(newTestEngine(&mockLLMProvider{}))
	server := httptest.NewServer(http.HandlerFunc(adapter.Handler()))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(time.Second)
	for !adapter.AwarenessReady() {
		if time.Now().After(deadline) {
			t.Fatal("transport not ready")
		}
		time.Sleep(time.Millisecond)
	}
	for _, kind := range []string{"private", "group"} {
		// Synthetic protocol fixture IDs, not real users or groups.
		target := llm.AwarenessTargetFor("qq", kind, "700000091")
		parts := []proactive.AwarenessMessage{{Type: "plain", Text: "A new topic"}}
		if kind == "group" {
			parts = append([]proactive.AwarenessMessage{{Type: "mention_user", MentionUserID: "700000092"}}, parts...)
		}
		done := make(chan error, 1)
		go func() { done <- adapter.SendAwareness(context.Background(), target, parts, nil) }()
		client.SetReadDeadline(time.Now().Add(time.Second))
		var action model.OneBotAction
		if err := client.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if action.Action != "send_"+kind+"_msg" {
			t.Fatalf("wrong route: %+v", action)
		}
		payload, _ := json.Marshal(action.Params.(map[string]any)["message"])
		if strings.Contains(string(payload), `"reply"`) {
			t.Fatal("invented triggering quote")
		}
		if kind == "group" && !strings.Contains(string(payload), `"at"`) {
			t.Fatal("lost native mention")
		}
		select {
		case <-done:
			t.Fatal("send completed before platform ACK")
		default:
		}
		if err := client.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": map[string]any{"message_id": 700000093}, "echo": action.Echo}); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("ACK not consumed")
		}
	}
}

func TestAwarenessOneBotRejectsMockAndAmbiguousConnections(t *testing.T) {
	a := NewAdapter(nil)
	mock := newWSConnection(nil)
	mock.mock = true
	a.conns[mock] = struct{}{}
	if a.AwarenessReady() {
		t.Fatal("selected browser mock")
	}
	a.conns[newWSConnection(nil)] = struct{}{}
	a.conns[newWSConnection(nil)] = struct{}{}
	if a.AwarenessReady() {
		t.Fatal("selected ambiguous transports")
	}
}

func TestGroupNameRefreshFailureRetainsDurableAndCachedNames(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	engine.GroupManager = memory.NewGroupManager(t.TempDir(), nil)
	store, err := engine.GroupManager.GetGroupStore("700000094")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateGroupName("Durable den"); err != nil {
		t.Fatal(err)
	}
	c := newWSConnection(nil)
	c.engine = engine
	c.groupCache[700000094] = cachedGroupInfo{Name: "", ExpiresAt: time.Now().Add(time.Minute)}
	if name := c.groupName(700000094); name != "Durable den" {
		t.Fatalf("negative cache erased durable name: %q", name)
	}
	c.groupCache[700000094] = cachedGroupInfo{Name: "Cached den", ExpiresAt: time.Now().Add(-time.Hour)}
	c.pendingGroupByID[700000094] = "fixture-echo"
	c.pendingGroupByEcho["fixture-echo"] = pendingGroupInfo{GroupID: 700000094, RequestedAt: time.Now()}
	if !c.handleAPIResponse([]byte(`{"echo":"fixture-echo","status":"failed","retcode":1}`)) {
		t.Fatal("response ignored")
	}
	if name := c.groupName(700000094); name != "Cached den" {
		t.Fatalf("refresh failure erased cached name: %q", name)
	}
}
