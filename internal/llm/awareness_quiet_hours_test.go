package llm

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

func TestAwarenessQuietHoursSkipAndRestartDeadlines(t *testing.T) {
	_, service, config, _ := awarenessFixture(t)
	if err := config.Update(proactive.AwarenessInterval, "1m - 1m", false); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"private", "group"} {
		service.Observe(AwarenessTargetFor("fixture-platform", kind, "fixture-target"))
	}
	for _, tc := range []struct {
		day, hour, minute, want int
	}{
		{2, 22, 58, 0}, {2, 22, 59, 2}, {2, 23, 0, 0},
		{3, 0, 0, 0}, {3, 6, 59, 0}, {3, 7, 0, 0}, {3, 7, 1, 2},
	} {
		now := time.Date(2026, time.January, tc.day, tc.hour, tc.minute, 0, 0, time.Local)
		service.now = func() time.Time { return now }
		due := service.dueTargets()
		if len(due) != tc.want {
			t.Fatalf("%s: got %d targets, want %d", now, len(due), tc.want)
		}
		for _, target := range due {
			service.clock.Done(target.SessionID)
		}
	}
}

func TestAwarenessQuietHoursBlockTriggerAndLateReplies(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		for _, stage := range []string{"trigger", "final", "tool"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				e, service, _, sends := awarenessFixture(t)
				target := AwarenessTargetFor("fixture-platform", kind, "fixture-target")
				now := time.Date(2026, time.January, 2, 22, 59, 0, 0, time.Local)
				if stage == "trigger" {
					now = now.Add(time.Minute)
				}
				service.now = func() time.Time { return now }
				args := `{"messages":[{"type":"plain","text":"must not send"}]}`
				tool := &proactiveMessageTool{structuredResultTool: structuredResultTool{staticTool: staticTool{name: "send_message"}, output: core.ToolExecutionResult{Content: args, MessagePayload: args}}}
				e.ToolRegistry = map[string]ToolExecutor{"send_message": tool}
				calls := 0
				e.Provider = injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
					calls++
					now = now.Add(time.Minute)
					if stage == "tool" && calls == 1 {
						return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "fixture-call", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: args}}}}}, nil
					}
					return finalInjection("must not send"), nil
				})
				service.Trigger(e.Context(), target)
				if stage == "trigger" && calls != 0 {
					t.Fatal("quiet trigger reached model")
				}
				if stage != "trigger" && calls == 0 {
					t.Fatal("did not exercise late reply")
				}
				if len(*sends) != 0 || len(e.SessionManager.GetOrCreate(target.SessionID).Snapshot()) != 0 {
					t.Fatal("quiet hours sent or recorded unsent content")
				}
			})
		}
	}
}

func TestAwarenessQuietHoursRecheckAfterQueue(t *testing.T) {
	e, service, _, sends := awarenessFixture(t)
	target := AwarenessTargetFor("fixture-platform", "private", "fixture-target")
	session := e.SessionManager.GetOrCreate(target.SessionID)
	previous := session.ReserveSequenceTurn("awareness", false, true)
	defer previous.Done()
	var clock atomic.Int64
	clock.Store(time.Date(2026, time.January, 2, 22, 59, 0, 0, time.Local).Unix())
	service.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	e.Provider = injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
		t.Error("queued trigger reached model during quiet hours")
		return finalInjection("must not send"), nil
	})
	done := make(chan struct{})
	go func() { defer close(done); service.Trigger(e.Context(), target) }()
	select {
	case <-previous.Context().Done(): // The next turn has reserved its place.
	case <-time.After(time.Second):
		t.Fatal("trigger did not enter queue")
	}
	clock.Add(60)
	previous.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queued trigger did not finish")
	}
	if len(*sends) != 0 {
		t.Fatal("queued trigger sent during quiet hours")
	}
}
