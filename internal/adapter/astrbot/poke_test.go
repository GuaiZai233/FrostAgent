package astrbot

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/modelrouter"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pokeNotice() Event {
	return Event{Type: "event", EventType: "notice", NoticeType: "notify", SubType: "poke", SelfID: "101", TargetID: "101", UserID: "202", GroupID: "404", MessageType: "group", Platform: "aiocqhttp", MessageID: "notice-test"}
}
func TestNormalizePokeFiltersAndCooldown(t *testing.T) {
	t.Setenv("ENABLE_POKE", "true")
	a := NewAdapter(newTestEngine(&mockLLMProvider{}))
	for name, change := range map[string]func(*Event){
		"other-target": func(e *Event) { e.TargetID = "303" }, "self-poke": func(e *Event) { e.UserID = e.SelfID },
		"missing-user": func(e *Event) { e.UserID = "" }, "missing-self": func(e *Event) { e.SelfID = "" },
		"missing-group": func(e *Event) { e.GroupID = "" }, "wrong-platform": func(e *Event) { e.Platform = "telegram" },
		"other-notice": func(e *Event) { e.SubType = "other" },
	} {
		e := pokeNotice()
		change(&e)
		if _, ok := a.normalizePoke(e, false); ok {
			t.Fatalf("accepted %s", name)
		}
	}
	e := pokeNotice()
	e.Metadata = map[string]any{"reply_message_id": "untrusted", "_frostagent_proactive_reply": true}
	e.Attachments = []core.Attachment{{Type: core.AttachmentTypeImage}}
	normalized, ok := a.normalizePoke(e, false)
	if !ok || normalized.Content != "（用户戳了戳你）" || normalized.pokeID == "" || normalized.replyMessageID() != "" || normalized.Metadata != nil || len(normalized.Attachments) > 0 {
		t.Fatalf("bad normalization: %+v", normalized)
	}
	e.UserID = "303"
	if _, ok := a.normalizePoke(e, false); ok {
		t.Fatal("different actor bypassed cooldown")
	}
	e.GroupID = "405"
	if _, ok := a.normalizePoke(e, false); !ok {
		t.Fatal("other group throttled")
	}
	t.Setenv("ENABLE_POKE", "false")
	e.GroupID = "406"
	if _, ok := a.normalizePoke(e, false); ok {
		t.Fatal("disabled notice accepted")
	}
}
func TestWSPokeWakeAndTerminalCooldown(t *testing.T) {
	t.Setenv("ENABLE_POKE", "true")
	t.Setenv("POKE_CUSTOM_TEXT", "的耳朵")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	e := pokeNotice()
	for i := 0; i < 2; i++ {
		if err := conn.WriteJSON(e); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var action Action
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if action.Action != "send_message" {
				t.Fatalf("missing reply: %+v", action)
			}
			for _, part := range action.Messages {
				if part.Type == "quote" {
					t.Fatalf("quoted notice: %+v", action)
				}
			}
		} else if action.Action != "noop" || !action.SuppressLLM {
			t.Fatalf("missing terminal suppression: %+v", action)
		}
		e.UserID = "303"
		e.MessageID = "notice-other"
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 1 {
		t.Fatalf("LLM calls=%d", len(provider.requests))
	}
	found := false
	for _, msg := range provider.requests[0].Messages {
		if msg.Role == core.RoleUser && strings.Contains(fmt.Sprint(msg.Content), "（用户戳了戳你 的耳朵）") {
			found = true
		}
	}
	if !found {
		t.Fatalf("poke user text missing: %+v", provider.requests[0].Messages)
	}
}

func TestWSPokeDisabledRouteFinishes(t *testing.T) {
	t.Setenv("ENABLE_POKE", "true")
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.ModelRouter = modelrouter.New(filepath.Join(t.TempDir(), "router.json"))
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(pokeNotice()); err != nil {
		t.Fatal(err)
	}
	action := readBotLoopAction(t, conn)
	if action.Action != "noop" || !action.SuppressLLM || action.Echo != "reply_notice-test" {
		t.Fatalf("disabled route must terminate poke: %+v", action)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 0 {
		t.Fatal("disabled route invoked LLM")
	}
}
func TestWSPokePreservesFilteredHumanInterrupt(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"ENABLE_POKE": "true", "POKE_COOLDOWN_SECONDS": "0",
		"BOT_LOOP_USER_IDS": "aiocqhttp:202", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
	})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for i := 0; i < 3; i++ {
		event := pokeNotice()
		event.MessageID = fmt.Sprintf("poke-test-%d", i)
		event.BotLoopInterrupted = i == 2
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
		action := readBotLoopAction(t, conn)
		want := "send_message"
		if i == 1 {
			want = "noop"
		}
		if action.Action != want {
			t.Fatalf("turn %d: %+v", i, action)
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 2 {
		t.Fatalf("LLM calls=%d", len(provider.requests))
	}
}
func TestWSPokeBillingAndFailureClosed(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprint(unavailable), func(t *testing.T) {
			t.Setenv("ENABLE_POKE", "true")
			var reserves, commits atomic.Int32
			bills := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/billing/llm/reservations" {
					var req billing.LLMReserveRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.Platform != "qq" || req.ExternalID != "202" || !strings.Contains(req.TaskID, "poke_") {
						t.Errorf("wrong billed principal/task: %+v", req)
					}
					reserves.Add(1)
					if unavailable {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"data": billing.LLMReservationResult{ReservationID: "poke-res", Decision: billing.DecisionReserved, Status: billing.StatusReserved, BalanceMinor: 10000}})
				} else if strings.HasSuffix(r.URL.Path, "/commit") {
					commits.Add(1)
					json.NewEncoder(w).Encode(map[string]any{"data": billing.LLMReservationResult{ReservationID: "poke-res", Decision: billing.DecisionReserved, Status: billing.StatusCommitted, BalanceMinor: 9999}})
				} else {
					http.NotFound(w, r)
				}
			}))
			defer bills.Close()
			provider := &mockLLMProvider{}
			engine := newTestEngine(provider)
			engine.BillingConfig = billing.Config{Enabled: true, Timeout: time.Second, MaxOutputTokens: 128, SafetyMultiplier: 1.2}
			engine.BillingClient = billing.NewClient(bills.URL, "synthetic-token", time.Second)
			srv, _, wsURL := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.WriteJSON(pokeNotice()); err != nil {
				t.Fatal(err)
			}
			action := readBotLoopAction(t, conn)
			if action.Action != "send_message" {
				t.Fatalf("missing terminal result: %+v", action)
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			want := 1
			if unavailable {
				want = 0
			}
			if len(provider.requests) != want || int(commits.Load()) != want || reserves.Load() != 1 {
				t.Fatalf("billing failure boundary: model=%d reserves=%d commits=%d", len(provider.requests), reserves.Load(), commits.Load())
			}
		})
	}
}
