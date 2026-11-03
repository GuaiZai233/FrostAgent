package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

func TestProactiveInboundStickerBackendSilence(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	t.Setenv(sticker.ActivityModeEnvKey, sticker.ActivityBotTurns)
	for _, mock := range []bool{false, true} {
		provider := &stickerActivityProvider{}
		engine := &Engine{Provider: provider, SessionManager: NewSessionManager()}
		session := engine.SessionManager.GetOrCreate("synthetic-group")
		turn := session.ReserveSequenceTurn("proactive", true, true)
		turn.Wait()
		messages, _, _ := session.PrepareTurnMessages(turn, "[图片]", "request-only prompt", nil)
		result := engine.RunMessagesWithContext(messages, RunContext{
			Turn: turn, SessionID: "synthetic-group", Epoch: turn.Epoch(), Proactive: true, ProactiveStickerOnly: true, Mock: mock,
			SendHook: func(string) error { t.Fatal("backend silence delivered a message"); return nil },
		})
		turn.Done()
		if !result.Silent || !result.StaySilentCalled || result.SilenceReason != "stay_silent" || result.Error != nil || result.Content != "" || result.Usage != (core.Usage{}) {
			t.Fatalf("unexpected result: %+v", result)
		}
		if len(provider.requests) != 0 {
			t.Fatal("backend silence called the provider")
		}
		calls := 0
		for _, message := range result.Transcript {
			for _, call := range message.ToolCalls {
				if call.Function.Name != StaySilentToolName || call.Function.Arguments != "{}" {
					t.Fatalf("unexpected backend tool: %+v", call)
				}
				calls++
			}
		}
		if calls != 1 || result.Transcript[len(result.Transcript)-1].Role != "tool" {
			t.Fatalf("want one completed stay_silent, got %+v", result.Transcript)
		}
		if history := session.Snapshot(); len(history) != 1 || history[0].Content != "[图片]" {
			t.Fatalf("durable history polluted: %+v", history)
		}
		_, remaining, _ := engine.stickerActivitySnapshot()
		want := 4
		if mock {
			want = 5
		}
		if remaining != want {
			t.Fatalf("remaining=%d want=%d", remaining, want)
		}
	}
}

func TestProactiveInboundStickerRequiresBothFlags(t *testing.T) {
	for _, flags := range []RunContext{{}, {Proactive: true}, {ProactiveStickerOnly: true}} {
		provider := &stickerActivityProvider{}
		engine := &Engine{Provider: provider, MaxIterations: 1}
		result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "ordinary image"}}, flags)
		if len(provider.requests) != 1 || result.Silent || result.StaySilentCalled {
			t.Fatalf("flags=%+v result=%+v", flags, result)
		}
	}
}

func TestProactiveInboundStickerCanceledOrStale(t *testing.T) {
	engine := &Engine{SessionManager: NewSessionManager()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := engine.RunMessagesWithContext(nil, RunContext{Context: ctx, Proactive: true, ProactiveStickerOnly: true})
	if !errors.Is(result.Error, context.Canceled) || result.StaySilentCalled {
		t.Fatalf("canceled result=%+v", result)
	}
	session := engine.SessionManager.GetOrCreate("synthetic-group")
	result = engine.RunMessagesWithContext(nil, RunContext{SessionID: "synthetic-group", Epoch: session.Epoch() + 1, Proactive: true, ProactiveStickerOnly: true})
	if result.SilenceReason != "epoch_changed" || result.StaySilentCalled {
		t.Fatalf("stale result=%+v", result)
	}
}

func TestProactiveInboundStickerWithoutTurn(t *testing.T) {
	provider := &stickerActivityProvider{}
	engine := &Engine{Provider: provider}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "[图片]"}}, RunContext{
		Proactive: true, ProactiveStickerOnly: true,
	})
	if !result.Silent || !result.StaySilentCalled || result.Error != nil || result.MemoryWritten || result.SilenceReason != "stay_silent" {
		t.Fatalf("nil-turn result=%+v", result)
	}
	if len(provider.requests) != 0 || len(result.Transcript) != 3 || result.Transcript[2].Role != "tool" {
		t.Fatalf("nil-turn silence requests=%d transcript=%+v", len(provider.requests), result.Transcript)
	}
}
