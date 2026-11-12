package astrbot

import (
	"context"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/gorilla/websocket"
)

func TestAwarenessAstrBotKeepsPlatformAndNativeMentions(t *testing.T) {
	server, adapter, url := startWSTestServer(newTestEngine(&mockLLMProvider{}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
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
	for _, platform := range []string{"aiocqhttp", "fixture-platform"} {
		for _, kind := range []string{"private", "group"} {
			target := llm.AwarenessTargetFor(platform, kind, "fixture-target")
			parts := []proactive.AwarenessMessage{{Type: "plain", Text: "new topic"}}
			if kind == "group" {
				parts = append([]proactive.AwarenessMessage{{Type: "mention_user", MentionUserID: "fixture-member"}}, parts...)
			}
			if err := adapter.SendAwareness(context.Background(), target, parts, nil); err != nil {
				t.Fatal(err)
			}
			client.SetReadDeadline(time.Now().Add(time.Second))
			var action Action
			if err := client.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Platform != platform || action.MessageType != kind || action.TargetID != target.ID || len(action.Messages) != len(parts) {
				t.Fatalf("lost transport route: %+v", action)
			}
			if action.hasQuote() {
				t.Fatal("invented triggering quote")
			}
			if kind == "group" && !action.hasMention("fixture-member") {
				t.Fatal("lost mention")
			}
			if kind == "group" && action.UserID != "" {
				t.Fatal("borrowed last sender")
			}
		}
	}
}

func TestAwarenessAstrBotRejectsMockAndAmbiguousConnections(t *testing.T) {
	a := NewAdapter(nil)
	mock := newWSConn(nil)
	mock.mock = true
	a.conns[mock] = struct{}{}
	if a.AwarenessReady() {
		t.Fatal("selected browser mock")
	}
	a.conns[newWSConn(nil)] = struct{}{}
	a.conns[newWSConn(nil)] = struct{}{}
	if a.AwarenessReady() {
		t.Fatal("selected ambiguous transports")
	}
}
