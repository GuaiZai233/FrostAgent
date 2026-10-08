package astrbot

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
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

func TestShouldReplyWithRNG(t *testing.T) {
	groupEvent := Event{
		Type:        "event",
		EventType:   "message",
		MessageType: "group",
		GroupID:     "10001",
		UserID:      "20001",
		Content:     "今天天气真好",
	}

	t.Run("disabled by default", func(t *testing.T) {
		scope := newTestScopeWithEnv(t, nil)
		reply := shouldReplyWithRNG(&groupEvent, func() float64 { return 0.01 }, scope)
		if reply {
			t.Fatalf("expected shouldReply to be false when proactive reply is disabled")
		}
	})

	t.Run("proactive reply hits probability", func(t *testing.T) {
		ev := groupEvent
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.40",
		})
		reply := shouldReplyWithRNG(&ev, func() float64 { return 0.20 }, scope)
		if !reply {
			t.Fatalf("expected shouldReply to be true when proactive roll hits")
		}
		if ev.Metadata == nil || ev.Metadata["_frostagent_proactive_reply"] != true {
			t.Fatalf("expected _frostagent_proactive_reply to be true in metadata, got %+v", ev.Metadata)
		}
	})

	t.Run("proactive reply misses probability", func(t *testing.T) {
		ev := groupEvent
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "0.40",
		})
		reply := shouldReplyWithRNG(&ev, func() float64 { return 0.60 }, scope)
		if reply {
			t.Fatalf("expected shouldReply to be false when proactive roll misses")
		}
	})

	t.Run("suppressed by GROUP_REPLY_ON_MENTION=false", func(t *testing.T) {
		ev := groupEvent
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
			"GROUP_REPLY_ON_MENTION":      "false",
		})
		reply := shouldReplyWithRNG(&ev, func() float64 { return 0.01 }, scope)
		if reply {
			t.Fatalf("expected shouldReply to be false when GROUP_REPLY_ON_MENTION=false")
		}
	})

	t.Run("explicit wake does not flag proactive", func(t *testing.T) {
		wakeEvent := Event{
			Type:        "event",
			EventType:   "message",
			MessageType: "group",
			GroupID:     "10001",
			UserID:      "20001",
			IsWake:      true,
			Content:     "今天天气真好",
		}
		scope := newTestScopeWithEnv(t, map[string]string{
			"PROACTIVE_REPLY_PROBABILITY": "1.00",
		})
		reply := shouldReplyWithRNG(&wakeEvent, func() float64 { return 0.01 }, scope)
		if !reply {
			t.Fatalf("expected shouldReply to be true for explicit wake")
		}
		if wakeEvent.Metadata != nil && wakeEvent.Metadata["_frostagent_proactive_reply"] == true {
			t.Fatalf("expected explicit wake not to be flagged as proactive")
		}
	})
}
