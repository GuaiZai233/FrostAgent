package onebot

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/model"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"strings"
	"testing"
	"time"
)

// All numeric identities below are small synthetic test fixtures.
func pokeNotice() model.OneBotEvent {
	return model.OneBotEvent{SelfID: 101, TargetID: 101, UserID: 202, GroupID: 404, PostType: "notice", NoticeType: "notify", SubType: "poke"}
}
func TestNormalizePokeFiltersAndSharedCooldown(t *testing.T) {
	t.Setenv("ENABLE_POKE", "true")
	t.Setenv("POKE_CUSTOM_TEXT", "的耳朵")
	engine := newTestEngine(&mockLLMProvider{})
	a := NewAdapter(engine)
	for name, change := range map[string]func(*model.OneBotEvent){
		"other-target": func(e *model.OneBotEvent) { e.TargetID = 303 },
		"self-poke":    func(e *model.OneBotEvent) { e.UserID = e.SelfID },
		"missing-user": func(e *model.OneBotEvent) { e.UserID = 0 },
		"missing-self": func(e *model.OneBotEvent) { e.SelfID = 0 },
		"other-notice": func(e *model.OneBotEvent) { e.SubType = "other" },
	} {
		e := pokeNotice()
		change(&e)
		if _, ok := a.normalizePoke(e, false); ok {
			t.Fatalf("accepted %s", name)
		}
	}
	if _, ok := a.normalizePoke(pokeNotice(), true); ok {
		t.Fatal("mock notice accepted")
	}
	normalized, ok := a.normalizePoke(pokeNotice(), false)
	if !ok || normalized.PostType != "message" || normalized.PokeID == "" || normalized.MessageID != 0 {
		t.Fatalf("bad normalization: %+v", normalized)
	}
	var message string
	if err := json.Unmarshal(normalized.Message, &message); err != nil || message != "（用户戳了戳你 的耳朵）" {
		t.Fatalf("message=%s, err=%v", normalized.Message, err)
	}
	signals := DetectGroupWakeSignals(normalized)
	if !signals.Poke || !signals.Explicit() || signals.Proactive {
		t.Fatalf("bad wake signals: %+v", signals)
	}
	other := pokeNotice()
	other.UserID = 303
	if _, ok := NewAdapter(engine).normalizePoke(other, false); ok {
		t.Fatal("different actor/connection bypassed cooldown")
	}
	other.GroupID = 405
	if _, ok := a.normalizePoke(other, false); !ok {
		t.Fatal("other group throttled")
	}
	other.GroupID = 0
	if _, ok := a.normalizePoke(other, false); !ok {
		t.Fatal("private session throttled")
	}
	t.Setenv("ENABLE_POKE", "false")
	other.GroupID = 406
	if _, ok := a.normalizePoke(other, false); ok {
		t.Fatal("disabled poke accepted")
	}
}
func TestWSPokeWakesAndBillsActor(t *testing.T) {
	t.Setenv("ENABLE_POKE", "true")
	t.Setenv("POKE_CUSTOM_TEXT", "的耳朵")
	t.Setenv("POKE_COOLDOWN_SECONDS", "0")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")
	t.Setenv("ENABLE_PROACTIVE_REPLY", "false")
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	billSrv, state := mockAlcyoneBillingServer(t)
	defer billSrv.Close()
	var tasks []string
	state.reserveHandler = func(w http.ResponseWriter, r *http.Request) {
		var req billing.LLMReserveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Platform != "qq" || req.ExternalID != "202" {
			t.Errorf("wrong billing actor: %+v", req)
		}
		tasks = append(tasks, req.TaskID)
		state.reserveCount++
		json.NewEncoder(w).Encode(map[string]any{"data": billing.LLMReservationResult{ReservationID: "poke-reservation", Decision: billing.DecisionReserved, Status: billing.StatusReserved, BalanceMinor: 10000}})
	}
	engine.BillingConfig = billing.Config{Enabled: true, Timeout: time.Second, MaxOutputTokens: 128, SafetyMultiplier: 1.2}
	engine.BillingClient = billing.NewClient(billSrv.URL, "synthetic-token", time.Second)
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for range 2 {
		if err := conn.WriteJSON(pokeNotice()); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			var action model.OneBotAction
			if err := json.Unmarshal(raw, &action); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"type":"reply"`) {
				t.Fatalf("quoted nonexistent notice: %s", raw)
			}
			data := map[string]any{"message_id": 505}
			if action.Action == "get_group_info" {
				data = map[string]any{"group_id": 404, "group_name": "synthetic-group"}
			}
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": data}); err != nil {
				t.Fatal(err)
			}
			if action.Action == "send_group_msg" {
				break
			}
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 2 {
		t.Fatalf("LLM requests=%d", len(provider.requests))
	}
	for _, req := range provider.requests {
		found := false
		for _, msg := range req.Messages {
			if msg.Role == core.RoleUser && strings.Contains(fmt.Sprint(msg.Content), "（用户戳了戳你 的耳朵）") {
				found = true
			}
		}
		if !found {
			t.Fatalf("poke text missing: %+v", req.Messages)
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.reserveCount != 2 || state.commitCount != 2 || len(tasks) != 2 || tasks[0] == tasks[1] {
		t.Fatalf("billing mismatch: reserves=%d commits=%d tasks=%v", state.reserveCount, state.commitCount, tasks)
	}
}
