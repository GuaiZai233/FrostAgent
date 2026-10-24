package llm

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/memory"
)

type injectionProvider func(context.Context, core.ChatRequest) (*core.ChatResponse, error)

func (f injectionProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	return f(ctx, req)
}

type injectionTool struct {
	name    string
	execute func() (string, error)
}

func (t injectionTool) Name() string                   { return t.name }
func (t injectionTool) Description() string            { return "synthetic tool" }
func (t injectionTool) Parameters() map[string]any     { return nil }
func (t injectionTool) Execute(string) (string, error) { return t.execute() }

func receiveInjection[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for execution")
		var zero T
		return zero
	}
}

func injectionRun(e *Engine, s *SessionContext, turn *SessionTurn, input string, state *BillingRunState) AgentRunResult {
	turn.Wait()
	messages, state, _ := s.PrepareTurnMessages(turn, input, input+" transient-background", state)
	result := e.RunMessagesWithContext(messages, RunContext{SessionID: s.ID(), Epoch: turn.Epoch(), Turn: turn, Billing: state, Mock: state == nil})
	turn.Done()
	return result
}

func injectionText(req core.ChatRequest) string {
	return fmt.Sprint(req.Messages[len(req.Messages)-1].Content)
}
func finalInjection(text string) *core.ChatResponse {
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: text}}
}
func injectionCalls(names ...string) *core.ChatResponse {
	r := finalInjection("")
	for _, name := range names {
		r.Message.ToolCalls = append(r.Message.ToolCalls, core.ToolCall{ID: name, Type: "function", Function: core.ToolCallFunction{Name: name, Arguments: "{}"}})
	}
	return r
}

func TestSequenceInterruptsOnlySenderAndDiscardsLateModel(t *testing.T) {
	e := &Engine{SessionManager: NewSessionManager(), MaxIterations: 2}
	s := e.SessionManager.GetOrCreate("group:synthetic-room")
	startedA, startedB := make(chan struct{}), make(chan context.Context, 1)
	late, finishB, providerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	e.ToolRegistry = map[string]ToolExecutor{"late-tool": injectionTool{"late-tool", func() (string, error) { calls.Add(1); return "late", nil }}}
	e.Provider = injectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		switch {
		case strings.HasPrefix(injectionText(req), "A-one"):
			close(startedA)
			<-late
			defer close(providerDone)
			return injectionCalls("late-tool"), nil
		case strings.HasPrefix(injectionText(req), "B-one"):
			startedB <- ctx
			<-finishB
			return finalInjection("B-done"), nil
		default:
			all := fmt.Sprint(req.Messages)
			if !strings.Contains(all, "A-one") || !strings.Contains(all, "B-one") || strings.Contains(all, "late-tool") {
				t.Errorf("bad continuation: %s", all)
			}
			return finalInjection("A-done"), nil
		}
	})
	a1 := s.ReserveSequenceTurn("sender:A", true, false)
	ra := make(chan AgentRunResult, 1)
	go func() { ra <- injectionRun(e, s, a1, "A-one", nil) }()
	receiveInjection(t, startedA)
	b := s.ReserveSequenceTurn("sender:B", true, false)
	rb := make(chan AgentRunResult, 1)
	go func() { rb <- injectionRun(e, s, b, "B-one", nil) }()
	bctx := receiveInjection(t, startedB)
	a2 := s.ReserveSequenceTurn("sender:A", true, false)
	if result := receiveInjection(t, ra); result.Content != "" || !result.Silent || result.LLMFailed {
		t.Fatalf("old result escaped: %+v", result)
	}
	if bctx.Err() != nil || b.Interrupted() {
		t.Fatal("A interrupted B")
	}
	if result := injectionRun(e, s, a2, "A-two", nil); result.Content != "A-done" {
		t.Fatalf("resumed: %+v", result)
	}
	close(late)
	receiveInjection(t, providerDone)
	close(finishB)
	if result := receiveInjection(t, rb); result.Content != "B-done" {
		t.Fatalf("B failed: %+v", result)
	}
	if calls.Load() != 0 {
		t.Fatal("late model executed a tool")
	}
}

func TestSequencePartialToolBatchAndResetBudget(t *testing.T) {
	e := &Engine{SessionManager: NewSessionManager(), MaxIterations: 2}
	s := e.SessionManager.GetOrCreate("private:synthetic-user")
	blocked, release, toolDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var completed, skipped, requestCount atomic.Int32
	e.ToolRegistry = map[string]ToolExecutor{
		"completed": injectionTool{"completed", func() (string, error) { completed.Add(1); return "completed-result", nil }},
		"blocked": injectionTool{"blocked", func() (string, error) {
			close(blocked)
			<-release
			defer close(toolDone)
			return "discard-this-late-result", nil
		}},
		"skipped": injectionTool{"skipped", func() (string, error) { skipped.Add(1); return "skipped", nil }},
		"next":    injectionTool{"next", func() (string, error) { return "next-result", nil }},
	}
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		switch requestCount.Add(1) {
		case 1:
			return injectionCalls("completed", "blocked", "skipped"), nil
		case 2:
			all := fmt.Sprint(req.Messages)
			for _, want := range []string{"completed-result", "outcome unknown", "not started", "new-input"} {
				if !strings.Contains(all, want) {
					t.Errorf("missing %q in %s", want, all)
				}
			}
			if strings.Contains(all, "discard-this-late-result") {
				t.Error("late tool result leaked")
			}
			return injectionCalls("next"), nil
		default:
			return finalInjection("finished"), nil
		}
	})
	first := s.ReserveSequenceTurn("sender:A", true, false)
	old := make(chan AgentRunResult, 1)
	go func() { old <- injectionRun(e, s, first, "old-input", nil) }()
	receiveInjection(t, blocked)
	next := s.ReserveSequenceTurn("sender:A", true, false)
	receiveInjection(t, old)
	if result := injectionRun(e, s, next, "new-input", nil); result.Content != "finished" {
		t.Fatalf("budget did not reset: %+v", result)
	}
	close(release)
	receiveInjection(t, toolDone)
	if completed.Load() != 1 || skipped.Load() != 0 || requestCount.Load() != 3 {
		t.Fatalf("replayed/skipped work: %d %d %d", completed.Load(), skipped.Load(), requestCount.Load())
	}
	for _, msg := range s.Snapshot() {
		if msg.Role == "tool" || len(msg.ToolCalls) > 0 {
			t.Fatal("private transcript entered shared history")
		}
	}
}

func TestSequenceCoalescesInputsAndRefreshesBackground(t *testing.T) {
	e := &Engine{SessionManager: NewSessionManager()}
	s := e.SessionManager.GetOrCreate("group:ordered")
	var count atomic.Int32
	e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		count.Add(1)
		all := fmt.Sprint(req.Messages)
		if strings.Count(all, "transient-background") != 1 {
			t.Errorf("duplicated background: %s", all)
		}
		if strings.Index(all, "first") > strings.Index(all, "second") || strings.Index(all, "second") > strings.Index(all, "third") {
			t.Errorf("wrong input order: %s", all)
		}
		return finalInjection("done"), nil
	})
	first := s.ReserveSequenceTurn("A", true, false)
	second := s.ReserveSequenceTurn("A", true, false)
	third := s.ReserveSequenceTurn("A", true, false)
	injectionRun(e, s, first, "first", nil)
	injectionRun(e, s, second, "second", nil)
	injectionRun(e, s, third, "third", nil)
	if count.Load() != 1 {
		t.Fatalf("coalesced inputs called model %d times", count.Load())
	}
}

func TestSequenceSkillsProactiveResetAndHistoryOwnership(t *testing.T) {
	s := NewSessionManager().GetOrCreate("group:skills")
	a := s.ReserveSequenceTurn("A", true, false)
	_, _, aID := s.PrepareTurnMessages(a, "A", "A", nil)
	if err := a.SkillSession().ActivateSkill("test", "revision", "A-args", a.Epoch()); err != nil {
		t.Fatal(err)
	}
	b := s.ReserveSequenceTurn("B", true, false)
	s.PrepareTurnMessages(b, "B", "B", nil)
	if b.SkillSession().HasActiveSkill("test", "revision") {
		t.Fatal("skills crossed sender boundary")
	}
	p := s.ReserveSequenceTurn("proactive", true, true)
	s.PrepareTurnMessages(p, "p", "p", nil)
	p.saveTranscript([]ChatMessage{{Role: "user", Content: "A"}, {Role: "user", Content: "B"}, {Role: "user", Content: "p"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "private-proactive"}}}})
	p2 := s.ReserveSequenceTurn("proactive", true, true)
	p.memoryState(true)
	if a.Interrupted() || b.Interrupted() || !p.Interrupted() {
		t.Fatal("proactive lane isolation failed")
	}
	p.Done()
	p2.Wait()
	m, _, _ := s.PrepareTurnMessages(p2, "p2", "p2", nil)
	if p2.memoryState(false) {
		t.Fatal("proactive replacement inherited previous memory result")
	}
	if strings.Contains(fmt.Sprint(m), "private-proactive") {
		t.Fatal("proactive inherited old trajectory")
	}
	s.DropMessage(aID)
	if strings.Contains(fmt.Sprint(s.Snapshot()), "{user A ") {
		t.Fatal("owned input not removed")
	}
	a2 := s.ReserveSequenceTurn("A", true, false)
	a.Done()
	a2.Wait()
	s.PrepareTurnMessages(a2, "A2", "A2", nil)
	if !a2.SkillSession().HasActiveSkill("test", "revision") {
		t.Fatal("same sender lost skills")
	}
	a.SkillSession().ActivateSkill("late", "old", "late", a.Epoch())
	if a2.SkillSession().HasActiveSkill("late", "old") {
		t.Fatal("late attempt changed resumed skills")
	}
	if err := s.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	if !a2.Interrupted() || !b.Interrupted() || !p2.Interrupted() {
		t.Fatal("reset failed to cancel all lanes")
	}
	a2.Done()
	b.Done()
	p2.Done()
	fresh := s.ReserveSequenceTurn("A", true, false)
	defer fresh.Done()
	s.PrepareTurnMessages(fresh, "fresh", "fresh", nil)
	if fresh.SkillSession().HasActiveSkill("test", "revision") {
		t.Fatal("reset retained skills")
	}
}

func TestBeginRunOldCleanupCannotClearNewCancel(t *testing.T) {
	s := &SessionContext{}
	_, _, oldCleanup := s.BeginRun(context.Background())
	next, _, cleanup := s.BeginRun(context.Background())
	defer cleanup()
	oldCleanup()
	s.CancelActiveRun()
	if next.Err() == nil {
		t.Fatal("old cleanup detached new cancellation")
	}
}

func TestSequenceAuthorityDoesNotHoldCancellationAcrossIO(t *testing.T) {
	s := NewSessionManager().GetOrCreate("group:authority")
	first := s.ReserveSequenceTurn("A", true, false)
	started, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		first.AwaitDelivery(func() { _ = first.WithAuthority(func() error { close(started); <-release; return nil }) })
		first.Done()
		close(stopped)
	}()
	receiveInjection(t, started)
	reserved := make(chan *SessionTurn, 1)
	go func() { reserved <- s.ReserveSequenceTurn("A", true, false) }()
	next := receiveInjection(t, reserved)
	receiveInjection(t, stopped)
	next.Wait()
	b := s.ReserveSequenceTurn("B", true, false)
	if err := s.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	next.Done()
	b.Done()
	close(release)
	if err := first.WithAuthority(func() error { t.Error("new write admitted after revocation"); return nil }); err == nil {
		t.Fatal("missing cancellation")
	}
}

func TestSequenceInterruptedMemoryReadsDoNotCountAsWrites(t *testing.T) {
	for _, action := range []string{"search", "list", "reflect", "write"} {
		t.Run(action, func(t *testing.T) {
			e := &Engine{SessionManager: NewSessionManager(), MaxIterations: 2}
			s := e.SessionManager.GetOrCreate("private:memory-interrupt")
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			e.ToolRegistry = map[string]ToolExecutor{"memory": injectionTool{"memory", func() (string, error) { close(started); <-release; close(finished); return "late", nil }}}
			var calls atomic.Int32
			e.Provider = injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				if calls.Add(1) > 1 {
					return finalInjection("done"), nil
				}
				resp := injectionCalls("memory")
				resp.Message.ToolCalls[0].Function.Arguments = fmt.Sprintf(`{"action":%q}`, action)
				return resp, nil
			})
			first := s.ReserveSequenceTurn("A", true, false)
			old := make(chan AgentRunResult, 1)
			go func() { old <- injectionRun(e, s, first, "old", nil) }()
			receiveInjection(t, started)
			next := s.ReserveSequenceTurn("A", true, false)
			result := receiveInjection(t, old)
			resumed := injectionRun(e, s, next, "next", nil)
			close(release)
			receiveInjection(t, finished)
			if result.MemoryWritten != (action == "write") || resumed.MemoryWritten != (action == "write") {
				t.Fatalf("memory action %s: old=%v resumed=%v", action, result.MemoryWritten, resumed.MemoryWritten)
			}
		})
	}
}

func TestSequenceConfirmedPublicRepliesSurviveCancellation(t *testing.T) {
	s := NewSessionManager().GetOrCreate("group:public")
	turn := s.ReserveSequenceTurn("A", true, false)
	log := NewPublicReplyLog(s, turn.Epoch())
	log.Record("confirmed", nil)
	next := s.ReserveSequenceTurn("A", true, false)
	log.Finish(turn.Interrupted())
	turn.Done()
	log.Record("late-confirmed", func(live bool) {
		if live {
			t.Error("late confirmation joined closed attempt")
		}
	})
	next.Wait()
	messages, _, _ := s.PrepareTurnMessages(next, "next", "next", nil)
	if !strings.Contains(fmt.Sprint(messages), "confirmed") || !strings.Contains(fmt.Sprint(messages), "late-confirmed") {
		t.Fatalf("lost public delivery: %+v", messages)
	}
	next.Done()
	if err := s.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	log.Record("post-reset", nil)
	if len(s.Snapshot()) != 0 {
		t.Fatal("late confirmation revived reset history")
	}
}

func TestSequencePublicDeliveryCommitsBeforeResetBoundary(t *testing.T) {
	for _, phase := range []string{"intermediate", "final"} {
		t.Run(phase, func(t *testing.T) {
			s := NewSessionManager().GetOrCreate("group:reset-delivery")
			turn := s.ReserveSequenceTurn("A", true, false)
			log := NewPublicReplyLog(s, turn.Epoch())
			accepted, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			sideEffects := func() {
				close(accepted)
				<-release
				s.AppendGroupCompactMessage(GroupCompactMessage{Role: "assistant", Content: "old-visible"}, 20)
				s.EnqueuePendingTurn([]memory.PendingExtractionItem{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "old-visible"}}}, 3, 3)
			}
			go func() {
				if phase == "intermediate" {
					log.Record("old-visible", func(bool) { sideEffects() })
				} else {
					s.WithEpochCommit(turn.Epoch(), func() { log.CommitFinal("old-visible", false); sideEffects() })
				}
				close(done)
			}()
			receiveInjection(t, accepted)
			resetStarted, resetDone := make(chan struct{}), make(chan error, 1)
			go func() { close(resetStarted); resetDone <- s.ResetSession(nil) }()
			receiveInjection(t, resetStarted)
			select {
			case err := <-resetDone:
				t.Fatalf("reset crossed partial public commit: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			receiveInjection(t, done)
			if err := receiveInjection(t, resetDone); err != nil {
				t.Fatal(err)
			}
			if len(s.Snapshot()) != 0 || len(s.SnapshotGroupContext(20, 4000, "").RecentMessages) != 0 {
				t.Fatal("old public output revived after reset")
			}
			s.mu.Lock()
			pending := len(s.pendingTurns)
			s.mu.Unlock()
			if pending != 0 {
				t.Fatal("old extraction was admitted into new epoch")
			}
			if s.WithEpochCommit(turn.Epoch(), func() { t.Error("old final commit callback ran") }) {
				t.Fatal("stale epoch accepted")
			}
			turn.Done()
		})
	}
}
