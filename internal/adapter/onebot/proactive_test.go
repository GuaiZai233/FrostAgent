package onebot

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/model"
	"FrostAgent/internal/proactive"
	"FrostAgent/internal/runtimescope"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestScopeWithEnv(t *testing.T, kv map[string]string) *runtimescope.Scope {
	envPath := filepath.Join(t.TempDir(), ".env")
	var lines []string
	for k, v := range kv {
		lines = append(lines, k+"="+v)
	}
	_ = os.WriteFile(envPath, []byte(strings.Join(lines, "\n")), 0600)
	store, _ := instanceconfig.Open(envPath, false)
	return runtimescope.New(store, nil, logs.General)
}

func TestDetectGroupWakeSignalsProactive(t *testing.T) {
	groupEvent := model.OneBotEvent{
		PostType:    "message",
		MessageType: "group",
		GroupID:     10001,
		UserID:      20001,
		SelfID:      30001,
		Message:     json.RawMessage(`"大家今天过得怎么样？"`),
	}

	t.Run("disabled by default", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, nil)
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.001 }, scope)
		if signals.Proactive || signals.Any() {
			t.Fatalf("expected no wake signals, got %+v", signals)
		}
	})

	t.Run("group reply disabled", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.50",
			"GROUP_REPLY_ON_MENTION":      "false",
		})
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.1 }, scope)
		if signals.Proactive || signals.Any() {
			t.Fatalf("expected proactive reply to be suppressed by GROUP_REPLY_ON_MENTION=false")
		}
	})

	t.Run("hit proactive probability", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.35",
		})
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.20 }, scope)
		if !signals.Proactive || !signals.Any() {
			t.Fatalf("expected proactive signal to hit, got %+v", signals)
		}
	})

	t.Run("miss proactive probability", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.35",
		})
		signals := DetectGroupWakeSignalsWithRNG(groupEvent, func() float64 { return 0.50 }, scope)
		if signals.Proactive || signals.Any() {
			t.Fatalf("expected proactive signal to miss, got %+v", signals)
		}
	})

	t.Run("explicit at bot does not roll proactive", func(t *testing.T) {
		atEvent := model.OneBotEvent{
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			SelfID:      30001,
			Message:     json.RawMessage(`[{"type":"at","data":{"qq":"30001"}},{"type":"text","data":{"text":"你好"}}]`),
		}
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
		})
		signals := DetectGroupWakeSignalsWithRNG(atEvent, func() float64 { return 0.01 }, scope)
		if !signals.AtBot {
			t.Fatalf("expected AtBot signal")
		}
		if signals.Proactive {
			t.Fatalf("expected explicit AtBot not to be marked as Proactive")
		}
	})

	t.Run("explicit alias does not roll proactive", func(t *testing.T) {
		aliasEvent := model.OneBotEvent{
			PostType:    "message",
			MessageType: "group",
			GroupID:     10001,
			UserID:      20001,
			SelfID:      30001,
			Message:     json.RawMessage(`[{"type":"text","data":{"text":"霜降狐 你好呀"}}]`),
		}
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
		})
		signals := DetectGroupWakeSignalsWithRNG(aliasEvent, func() float64 { return 0.01 }, scope)
		if !signals.Alias {
			t.Fatalf("expected Alias signal")
		}
		if signals.Proactive {
			t.Fatalf("expected explicit Alias not to be marked as Proactive")
		}
	})
}

func TestBuildResponseContextWithProactive(t *testing.T) {
	event := model.OneBotEvent{
		MessageType: "group",
		GroupID:     10001,
	}
	ctxStr := buildResponseContext(event, GroupWakeSignals{Proactive: true}, false)
	if !strings.Contains(ctxStr, `"proactive"`) {
		t.Fatalf("expected triggers to include 'proactive', got: %s", ctxStr)
	}
	if !strings.Contains(ctxStr, proactive.PromptPrefix[:10]) && !strings.Contains(ctxStr, "stay_silent") {
		t.Fatalf("expected silence guidance in response context: %s", ctxStr)
	}
}
