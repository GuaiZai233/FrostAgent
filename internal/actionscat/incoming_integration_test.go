package actionscat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/astrbot"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type emptyProvider struct{ calls chan struct{} }

func (p emptyProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	if p.calls != nil {
		p.calls <- struct{}{}
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant}}, nil
}

type ingressAdapter interface {
	Handler() http.HandlerFunc
	SetIncomingDispatcher(core.IncomingDispatcher)
	CloseConnections()
}

func ingressCase(name, messageType, text string) (ingressAdapter, *llm.Engine, any) {
	engine := &llm.Engine{MaxIterations: 1, Provider: emptyProvider{}, SessionManager: llm.NewSessionManager()}
	if name == "onebot" {
		event := model.OneBotEvent{PostType: "message", MessageType: messageType, UserID: 1, SelfID: 3, MessageID: 1}
		if messageType == "group" {
			event.GroupID = 2
		}
		event.Message, _ = json.Marshal([]map[string]any{{"type": "text", "data": map[string]string{"text": text}}})
		return onebot.NewAdapter(engine), engine, event
	}
	groupID := ""
	if messageType == "group" {
		groupID = "synthetic-group"
	}
	return astrbot.NewAdapter(engine), engine, astrbot.Event{
		Type: "event", EventType: "message", Platform: "test-platform", UserID: "synthetic-user",
		MessageID: "synthetic-message", MessageType: messageType, GroupID: groupID, Content: text,
	}
}

func startIngress(t *testing.T, adapter ingressAdapter, mock bool) *websocket.Conn {
	t.Helper()
	server := httptest.NewServer(adapter.Handler())
	t.Cleanup(server.Close)
	t.Cleanup(adapter.CloseConnections)
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	if mock {
		url += "?mock=true"
	}
	connection, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func dispatchClient(endpoint string) *actionscat.Client {
	return actionscat.New(func(key string) string {
		if key == "ACTIONSCAT_ENDPOINT" {
			return endpoint
		}
		if key == "ACTIONSCAT_DISPATCH_TOKEN" {
			return "synthetic-dispatch-token"
		}
		return ""
	})
}

func TestIngressDispatchesPlainTextOnceWithoutDialogueWake(t *testing.T) {
	for _, name := range []string{"onebot", "astrbot"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			received := make(chan actionscat.MessageEvent, 2)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/dispatch" || r.Header.Get("Authorization") != "Bearer synthetic-dispatch-token" {
					http.Error(w, "wrong dispatch route/auth", http.StatusBadRequest)
					return
				}
				var event actionscat.MessageEvent
				if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				calls.Add(1)
				received <- event
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer backend.Close()
			adapter, engine, event := ingressCase(name, "group", "https://www.bilibili.com/video/test")
			llmCalls := make(chan struct{}, 1)
			engine.Provider = emptyProvider{calls: llmCalls}
			adapter.SetIncomingDispatcher(dispatchClient(backend.URL))
			connection := startIngress(t, adapter, false)
			if name == "onebot" {
				if err := connection.WriteJSON(model.OneBotEvent{PostType: "notice", MessageType: "group", UserID: 1, GroupID: 2, Message: json.RawMessage(`"notice"`)}); err != nil {
					t.Fatal(err)
				}
			} else if err := connection.WriteJSON(astrbot.Event{Type: "event", EventType: "notice", MessageType: "group", UserID: "synthetic-user", GroupID: "synthetic-group", Content: "notice"}); err != nil {
				t.Fatal(err)
			}
			if name == "astrbot" {
				if err := connection.WriteJSON(astrbot.Event{Type: "action", EventType: "message", MessageType: "group", UserID: "synthetic-user", GroupID: "synthetic-group", Content: "not an ingress event"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := connection.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			select {
			case message := <-received:
				if message.Text != "https://www.bilibili.com/video/test" || message.SessionID == "" || message.UserID == "" || message.GroupID == "" || message.Platform == "" {
					t.Fatalf("invalid event: %+v", message)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("ordinary group message was not dispatched")
			}
			_ = connection.Close()
			adapter.CloseConnections()
			if got := calls.Load(); got != 1 {
				t.Fatalf("dispatch attempts = %d, want 1", got)
			}
			select {
			case <-llmCalls:
				t.Fatal("unaddressed group event invoked dialogue LLM")
			default:
			}
		})
	}
}

func TestHardBotLoopPreservesIndependentIngressDispatch(t *testing.T) {
	for _, name := range []string{"onebot", "astrbot"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("BOT_LOOP_USER_IDS", "onebot:1,test-platform:synthetic-user")
			t.Setenv("BOT_LOOP_MAX_TURNS", "1")
			t.Setenv("BOT_LOOP_MODE", "hard")
			received := make(chan actionscat.MessageEvent, 2)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var event actionscat.MessageEvent
				if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				received <- event
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer backend.Close()
			adapter, engine, first := ingressCase(name, "private", "first")
			modelCalls := make(chan struct{}, 2)
			engine.Provider = emptyProvider{calls: modelCalls}
			adapter.SetIncomingDispatcher(dispatchClient(backend.URL))
			connection := startIngress(t, adapter, false)
			if err := connection.WriteJSON(first); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-received:
				if event.Text != "first" {
					t.Fatalf("first dispatch: %+v", event)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("first message did not reach ActionsCat")
			}
			select {
			case <-modelCalls:
			case <-time.After(2 * time.Second):
				t.Fatal("first message did not start a dialogue")
			}

			var second any
			switch event := first.(type) {
			case model.OneBotEvent:
				event.MessageID = 2
				event.Message, _ = json.Marshal([]map[string]any{{"type": "text", "data": map[string]string{"text": "second"}}})
				second = event
			case astrbot.Event:
				event.MessageID = "synthetic-second"
				event.Content = "second"
				second = event
			default:
				t.Fatalf("unexpected adapter event type %T", first)
			}
			if err := connection.WriteJSON(second); err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-received:
				if event.Text != "second" {
					t.Fatalf("over-limit dispatch: %+v", event)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("over-limit message was lost before ActionsCat dispatch")
			}
			select {
			case <-modelCalls:
				t.Fatal("over-limit message invoked the dialogue model")
			case <-time.After(150 * time.Millisecond):
			}
		})
	}
}

type filteringClassifier struct{}

func (filteringClassifier) Classify(context.Context, security.ClassificationInput) (security.ClassificationResult, error) {
	return security.ClassificationResult{Category: security.RiskCategoryPromptInjection, RiskLevel: security.RiskLevelHigh, Reason: "synthetic filter"}, nil
}

func TestDispatchUsesSanitizedIngressText(t *testing.T) {
	for _, name := range []string{"onebot", "astrbot"} {
		t.Run(name, func(t *testing.T) {
			received := make(chan actionscat.MessageEvent, 1)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var event actionscat.MessageEvent
				if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				received <- event
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer backend.Close()
			adapter, engine, event := ingressCase(name, "group", "synthetic unsafe text")
			engine.Security = security.NewController(t.TempDir())
			engine.Security.SetMode(security.ControlModeAggressive)
			engine.Security.SetClassifier(filteringClassifier{})
			adapter.SetIncomingDispatcher(dispatchClient(backend.URL))
			connection := startIngress(t, adapter, false)
			if err := connection.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			select {
			case message := <-received:
				if message.Text != security.SanitizedMessageForCategory(security.RiskCategoryPromptInjection) {
					t.Fatalf("dispatch did not use sanitized content: %+v", message)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("sanitized event was not dispatched")
			}
		})
	}
}

func TestDispatchFailurePreservesPrivateDialogue(t *testing.T) {
	for _, name := range []string{"onebot", "astrbot"} {
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				http.Error(w, "synthetic failure", http.StatusInternalServerError)
			}))
			defer backend.Close()
			adapter, engine, event := ingressCase(name, "private", "hello")
			calls := make(chan struct{}, 1)
			engine.Provider = emptyProvider{calls: calls}
			adapter.SetIncomingDispatcher(dispatchClient(backend.URL))
			connection := startIngress(t, adapter, false)
			if err := connection.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			select {
			case <-calls:
			case <-time.After(2 * time.Second):
				t.Fatal("dispatch failure prevented normal dialogue")
			}
			if attempts.Load() != 1 {
				t.Fatal("failed dispatch was retried")
			}
		})
	}
}

func TestMockAndLockedIngressNeverDispatch(t *testing.T) {
	for _, name := range []string{"onebot", "astrbot"} {
		for _, mock := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/mock=%t", name, mock), func(t *testing.T) {
				var attempts atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
				defer backend.Close()
				adapter, engine, event := ingressCase(name, "group", "hello")
				if !mock {
					engine.Security = security.NewController(t.TempDir())
					platform, user := "test-platform", "synthetic-user"
					if name == "onebot" {
						platform, user = "onebot", "1"
					}
					principal, err := security.NewPrincipal(platform, user)
					if err != nil {
						t.Fatal(err)
					}
					if err := engine.Security.Lock(principal, "synthetic ingress lock"); err != nil {
						t.Fatal(err)
					}
				}
				adapter.SetIncomingDispatcher(dispatchClient(backend.URL))
				connection := startIngress(t, adapter, mock)
				if err := connection.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				// A subsequent private event supplies a reader-loop barrier: mock reaches
				// dialogue, while a locked principal receives a direct rejection.
				_, _, private := ingressCase(name, "private", "barrier")
				if mock {
					calls := make(chan struct{}, 1)
					engine.Provider = emptyProvider{calls: calls}
					if err := connection.WriteJSON(private); err != nil {
						t.Fatal(err)
					}
					select {
					case <-calls:
					case <-time.After(2 * time.Second):
						t.Fatal("mock reader barrier timed out")
					}
				} else {
					if err := connection.WriteJSON(private); err != nil {
						t.Fatal(err)
					}
					_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
					if _, _, err := connection.ReadMessage(); err != nil {
						t.Fatal(err)
					}
				}
				if attempts.Load() != 0 {
					t.Fatal("mock/locked event reached ActionsCat")
				}
			})
		}
	}
}

func TestIngressCreatesRunThroughRealActionsCatMatcher(t *testing.T) {
	checkout := os.Getenv("ACTIONSCAT_TEST_CHECKOUT")
	if checkout == "" {
		t.Skip("set ACTIONSCAT_TEST_CHECKOUT to an ActionsCat checkout to run the real matcher integration")
	}
	fixtureDir, err := os.MkdirTemp(checkout, "frostagent-dispatch-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(fixtureDir)
	source, err := os.ReadFile(filepath.Join("..", "..", "eval", "actionscat_dispatch_fixture.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "main.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	executable := filepath.Join(data, "fixture.exe")
	build := exec.Command("go", "build", "-o", executable, "main.go")
	build.Dir = fixtureDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real ActionsCat fixture: %v\n%s", err, output)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := filepath.Join(data, "ready")
	process := exec.CommandContext(ctx, executable, data, ready)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = process.Wait() }()
	var endpoint string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if url, err := os.ReadFile(ready); err == nil {
			endpoint = string(url)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if endpoint == "" {
		t.Fatal("real matcher fixture did not start")
	}
	for index, name := range []string{"onebot", "astrbot"} {
		adapter, _, event := ingressCase(name, "group", "https://www.bilibili.com/video/test")
		adapter.SetIncomingDispatcher(dispatchClient(endpoint))
		connection := startIngress(t, adapter, false)
		if err := connection.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
		var runs []struct {
			ID         string            `json:"id"`
			PlannedEnv map[string]string `json:"planned_env"`
		}
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			response, err := http.Get(endpoint + "/test/runs")
			if err != nil {
				t.Fatal(err)
			}
			err = json.NewDecoder(response.Body).Decode(&runs)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) == index+1 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(runs) != index+1 || runs[0].ID == "" {
			t.Fatalf("%s inbound did not create exactly one real Run: %+v", name, runs)
		}
		found := false
		for _, run := range runs {
			if run.PlannedEnv["ACTIONSCAT_EVENT_PLATFORM"] == map[string]string{"onebot": "onebot", "astrbot": "test-platform"}[name] {
				found = run.PlannedEnv["ACTIONSCAT_EVENT_TEXT"] == "https://www.bilibili.com/video/test" && run.PlannedEnv["ACTIONSCAT_EVENT_GROUP_ID"] != "" && run.PlannedEnv["ACTIONSCAT_EVENT_USER_ID"] != "" && run.PlannedEnv["ACTIONSCAT_EVENT_ADAPTER_ID"] == name
			}
		}
		if !found {
			t.Fatalf("%s matcher Run lost routing/text: %+v", name, runs)
		}
		_ = connection.Close()
		adapter.CloseConnections()
	}
}
