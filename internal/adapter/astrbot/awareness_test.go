package astrbot

import (
	"context"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
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
	for _, platform := range []string{"qq", "onebot", "aiocqhttp", "fixture-platform"} {
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
			wantPlatform := platform
			if memory.IsQQPlatform(platform) {
				wantPlatform = "aiocqhttp"
			}
			if action.Platform != wantPlatform || action.MessageType != kind || action.TargetID != target.ID || len(action.Messages) != len(parts) {
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

func TestAwarenessAstrBotColdStartUsesQQTransportPlatform(t *testing.T) {
	for _, scenario := range []string{"persisted-group", "private-whitelist"} {
		t.Run(scenario, func(t *testing.T) {
			engine := newTestEngine(&mockLLMProvider{})
			values := map[string]string{
				proactive.AwarenessEnabled: "true", proactive.AwarenessInterval: "0.001m - 0.001m",
				proactive.AwarenessQuietHoursEnabled:       "false",
				proactive.AwarenessPrivateWhitelistEnabled: "true", proactive.AwarenessGroupWhitelistEnabled: "false",
			}
			kind := "group"
			if scenario == "private-whitelist" {
				kind = "private"
				values[proactive.AwarenessPrivateWhitelist] = "aiocqhttp:fixture-target"
			}
			engine.Scope = newTestScopeWithEnv(t, values)
			t.Cleanup(func() { engine.Scope.Cancel(); engine.Scope.Wait() })
			if scenario == "persisted-group" {
				dir := t.TempDir()
				manager := memory.NewGroupManager(dir, engine.Scope)
				store, err := manager.GetGroupStoreForPlatform("aiocqhttp", "fixture-target")
				if err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateGroupName("Persistent den"); err != nil {
					t.Fatal(err)
				}
				engine.GroupManager = memory.NewGroupManager(dir, engine.Scope)
				profiles, err := engine.GroupManager.ListGroups()
				if err != nil || len(profiles) != 1 || profiles[0].Platform != "qq" {
					t.Fatalf("expected canonical profile after restart: %+v, %v", profiles, err)
				}
			}
			server, adapter, url := startWSTestServer(engine)
			defer server.Close()
			client, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			engine.Awareness = &llm.AwarenessService{Engine: engine, Resolve: func(llm.AwarenessTarget) (llm.AwarenessSender, bool) {
				return adapter.SendAwareness, adapter.AwarenessReady()
			}}
			ctx, cancel := context.WithCancel(engine.Context())
			defer cancel()
			done := make(chan struct{})
			engine.Go(func() { defer close(done); engine.Awareness.Run(ctx) })
			// No event is written to the socket: the scheduler must resolve the
			// transport from durable identity or configuration alone.
			client.SetReadDeadline(time.Now().Add(4 * time.Second))
			var action Action
			if err := client.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Platform != "aiocqhttp" || action.TargetID != "fixture-target" || action.MessageType != kind || action.SessionID != kind+":fixture-target" || len(action.Messages) == 0 {
				t.Fatalf("cold-start route changed on the wire: %+v", action)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("awareness worker did not stop")
			}
		})
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
