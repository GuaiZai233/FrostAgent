package llm

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

func historyEngine(t *testing.T) (*Engine, context.Context, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: "fixture-instance", Name: "Fixture", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	e := &Engine{ToolHistory: toolhistory.NewStore(db, "fixture-instance")}
	return e, WithRunContext(ctx, RunContext{ActorPlatform: "qq", ActorUserID: "fixture-actor", MemorySource: memory.GroupMessage{MessageID: "fixture-source"}}), db
}

func TestToolHistoryKeepsUntruncatedOriginalAndDeliveryResult(t *testing.T) {
	e, ctx, _ := historyEngine(t)
	raw := strings.Repeat("原", MaxToolOutputBytes)
	tool := &structuredResultTool{staticTool: staticTool{name: "fixture_tool"}, output: core.ToolExecutionResult{Content: raw, RawResult: `{"content":[{"type":"image","data":"AQID"}]}`}}
	output, err := e.executeRecordedTool(ctx, tool, ToolCall{Function: ToolCallFunction{Name: tool.Name(), Arguments: `{}`}})
	if err != nil {
		t.Fatal(err)
	}
	content, err := e.finishToolHistory(ctx, output, "confirmed delivery", false)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := ToolHistoryScope(mustRun(ctx))
	record, err := e.ToolHistory.Read(ctx, scope, output.HistoryRefID)
	if err != nil || record.ResultRaw != raw || record.MCPResultRaw != tool.output.RawResult || record.ModelResultRaw != content || record.State != "complete" {
		t.Fatalf("archive changed execution/delivery result: %+v %v", record, err)
	}
}

func mustRun(ctx context.Context) RunContext { run, _ := RunContextFromContext(ctx); return run }

func TestToolHistoryContextBudgetPreservesPairsAndChat(t *testing.T) {
	first, second := toolhistory.NewRef(), toolhistory.NewRef()
	args := `{"value":"fixture"}`
	messages := []ChatMessage{
		{Role: "user", Content: "[msg_fixture] precise chat reference"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "fixture-call-a", Function: ToolCallFunction{Name: "fixture_tool", Arguments: args}}}},
		{Role: "tool", ToolCallID: "fixture-call-a", ToolHistoryRefID: first, Content: strings.Repeat("a", 40000)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "fixture-call-b", Function: ToolCallFunction{Name: "fixture_tool", Arguments: args}}}},
		{Role: "tool", ToolCallID: "fixture-call-b", ToolHistoryRefID: second, Content: strings.Repeat("b", 40000)},
	}
	compactArchivedToolMessages(messages)
	if len(messages) != 5 || messages[0].Content != "[msg_fixture] precise chat reference" || messages[1].ToolCalls[0].Function.Arguments != args || messages[2].ToolCallID != "fixture-call-a" || !strings.Contains(contentToText(messages[2].Content), first) || messages[4].Content != strings.Repeat("b", 40000) {
		t.Fatal("compaction broke chat references, pairs, or newest result")
	}
}

type historySlowTool struct {
	staticTool
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (t *historySlowTool) ExecuteResult(context.Context, string) (core.ToolExecutionResult, error) {
	t.calls.Add(1)
	close(t.entered)
	<-t.release
	return core.ToolExecutionResult{Content: "late external result"}, nil
}

func TestToolHistoryInterruptedExecutionRetainsLateOriginal(t *testing.T) {
	e, ctx, _ := historyEngine(t)
	ctx, cancel := context.WithCancel(ctx)
	tool := &historySlowTool{staticTool: staticTool{name: "fixture-slow"}, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan core.ToolExecutionResult, 1)
	go func() {
		output, _ := e.executeRecordedTool(ctx, tool, ToolCall{Function: ToolCallFunction{Name: tool.Name(), Arguments: `{}`}})
		done <- output
	}()
	<-tool.entered
	cancel()
	output := <-done
	if !toolhistory.ValidRef(output.HistoryRefID) {
		t.Fatal("interrupted execution lost reference")
	}
	close(tool.release)
	scope, _ := ToolHistoryScope(mustRun(ctx))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err := e.ToolHistory.Read(context.Background(), scope, output.HistoryRefID)
		if err == nil && r.State == "executed" {
			if r.ResultRaw != "late external result" || !r.Restricted || r.ForModel().ResultRaw != "" || tool.calls.Load() != 1 {
				t.Fatalf("late result changed or escaped review: %+v", r)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("late original was not archived")
}

func TestToolHistoryStorageFailureDoesNotExecuteTool(t *testing.T) {
	e, ctx, db := historyEngine(t)
	if _, err := db.SQL.Exec(`DROP TABLE tool_history`); err != nil {
		t.Fatal(err)
	}
	tool := &structuredResultTool{staticTool: staticTool{name: "fixture-tool"}, output: core.ToolExecutionResult{Content: "must not execute"}}
	var calls atomic.Int32
	counted := recordedTool{ToolExecutor: tool, execute: func(context.Context) (core.ToolExecutionResult, error) { calls.Add(1); return tool.output, nil }}
	if _, err := e.executeRecordedTool(ctx, counted, ToolCall{Function: ToolCallFunction{Name: tool.Name(), Arguments: `{}`}}); !errors.Is(err, errToolHistoryStorage) {
		t.Fatalf("archive outage ignored: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("external action executed without durable reference")
	}
}

func TestToolHistoryDoesNotExposePrivateErrorDiagnostics(t *testing.T) {
	e, ctx, _ := historyEngine(t)
	for _, test := range []struct {
		raw string
		err error
	}{
		{raw: `{"isError":true,"content":[{"type":"text","text":"fixture-private-diagnostic"}]}`},
		{err: errors.New("fixture-private-diagnostic")},
	} {
		tool := &structuredResultTool{staticTool: staticTool{name: "fixture-tool"}, output: core.ToolExecutionResult{Content: "safe error reference", RawResult: test.raw}, err: test.err}
		output, _ := e.executeRecordedTool(ctx, tool, ToolCall{Function: ToolCallFunction{Name: tool.Name(), Arguments: `{}`}})
		if _, err := e.finishToolHistory(ctx, output, "safe error reference", false); err != nil {
			t.Fatal(err)
		}
		scope, _ := ToolHistoryScope(mustRun(ctx))
		r, err := e.ToolHistory.Read(ctx, scope, output.HistoryRefID)
		if err != nil || !r.Restricted || r.ForModel().MCPResultRaw != "" || r.ForModel().Error != "" || !strings.Contains(r.ForModel().ModelResultRaw, "safe error reference") {
			t.Fatalf("diagnostics escaped original error boundary: %+v %v", r, err)
		}
	}
}

func TestToolHistoryArchivesActualDeliveryAndSecurityOutcome(t *testing.T) {
	for _, name := range []string{"delivered", "delivery failure", "security rejection", "classifier failure"} {
		t.Run(name, func(t *testing.T) {
			e, ctx, _ := historyEngine(t)
			run := mustRun(ctx)
			sends := 0
			run.SendHook = func(string) error {
				sends++
				if name == "delivery failure" {
					return errors.New("fixture-private-delivery-diagnostic")
				}
				return nil
			}
			tool := &structuredResultTool{staticTool: staticTool{name: "send_message"}, output: core.ToolExecutionResult{
				Content: "fixture-private-original", MessagePayload: `{"messages":[{"type":"plain","text":"fixture-message"}]}`,
			}}
			e.ToolRegistry = map[string]ToolExecutor{tool.Name(): tool}
			e.Provider = &sequentialProvider{responses: []core.ChatResponse{
				{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "fixture-call", Type: "function", Function: core.ToolCallFunction{Name: tool.Name(), Arguments: `{}`}}}}},
				{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "fixture-final"}},
			}}
			if name == "security rejection" || name == "classifier failure" {
				e.Security = security.NewController(t.TempDir())
				e.Security.SetMode(security.ControlModeAggressive)
				e.Security.Watchdog.SetClassifier(&mockGateClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					if input.Stage == security.StageToolResult {
						if name == "classifier failure" {
							return security.ClassificationResult{}, errors.New("fixture-private-classifier-diagnostic")
						}
						return security.ClassificationResult{Category: security.RiskCategoryDataExfiltration, RiskLevel: security.RiskLevelHigh}, nil
					}
					return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
				}})
			}
			result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "fixture-input"}}, run)
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			records, err := e.ToolHistory.Export(ctx)
			if err != nil || len(records) != 1 {
				t.Fatalf("archive missing: %v %v", records, err)
			}
			r := records[0]
			blocked := name == "security rejection" || name == "classifier failure"
			if r.State != "complete" || r.ResultRaw != tool.output.Content || r.Restricted != blocked || (sends == 0) != blocked {
				t.Fatalf("execution/security/delivery state changed: %+v sends=%d", r, sends)
			}
			if blocked && (r.ForModel().ResultRaw != "" || strings.Contains(r.ForModel().ModelResultRaw, "fixture-private")) {
				t.Fatal("historical read bypassed security isolation")
			}
			want := "消息已发送"
			if name == "delivery failure" {
				want = "消息发送失败"
			}
			if !blocked && !strings.Contains(r.ModelResultRaw, want) {
				t.Fatalf("archive confused message preparation and delivery: %+v", r)
			}
		})
	}
}

func TestToolHistoryStickerPairReferencesAndStorageFailures(t *testing.T) {
	for _, stage := range []string{"success", "before execution", "after execution", "after delivery"} {
		t.Run(stage, func(t *testing.T) {
			e, ctx, db := historyEngine(t)
			run := mustRun(ctx)
			calls, sends := 0, 0
			dropHistory := func() {
				if _, err := db.SQL.Exec(`DROP TABLE tool_history`); err != nil {
					t.Error(err)
				}
			}
			tool := recordedTool{ToolExecutor: &staticTool{name: "fixture-pair"}, execute: func(context.Context) (core.ToolExecutionResult, error) {
				calls++
				if stage == "after execution" {
					dropHistory()
				}
				payload := `{"messages":[{"type":"plain","text":"fixture-message"}]}`
				if calls == 2 {
					payload = `{"messages":[{"type":"image","path":"/fixture/sticker.png","is_sticker":true}]}`
				}
				return core.ToolExecutionResult{Content: "fixture-prepared", MessagePayload: payload}, nil
			}}
			e.ToolRegistry = map[string]ToolExecutor{"send_message": tool, "send_sticker": tool}
			run.SendHook = func(string) error {
				sends++
				if stage == "after delivery" {
					dropHistory()
				}
				return nil
			}
			ctx = WithRunContext(ctx, run)
			if stage == "before execution" {
				dropHistory()
			}
			call := func(name string) ToolCall {
				return ToolCall{ID: name + "-fixture", Function: ToolCallFunction{Name: name, Arguments: `{}`}}
			}
			text, sticker, err := e.deliverStickerPair(ctx, &run, call("send_message"), call("send_sticker"))
			switch stage {
			case "success":
				if err != nil || calls != 2 || sends != 1 || !toolhistory.ValidRef(text.ToolHistoryRefID) || !toolhistory.ValidRef(sticker.ToolHistoryRefID) || text.ToolHistoryRefID == sticker.ToolHistoryRefID {
					t.Fatalf("pair lost references/delivery: %+v %+v %v calls=%d sends=%d", text, sticker, err, calls, sends)
				}
				records, err := e.ToolHistory.Export(ctx)
				if err != nil || len(records) != 2 {
					t.Fatal("pair archives missing", err)
				}
				for _, r := range records {
					if r.State != "complete" || r.Restricted || !strings.Contains(r.ModelResultRaw, "消息已发送") {
						t.Fatalf("pair archive incomplete: %+v", r)
					}
				}
			case "before execution":
				if !errors.Is(err, errToolHistoryStorage) || calls != 0 || sends != 0 {
					t.Fatalf("pair continued after storage failure: %v calls=%d sends=%d", err, calls, sends)
				}
			case "after execution":
				if !errors.Is(err, errToolHistoryStorage) || calls != 1 || sends != 0 {
					t.Fatalf("pair continued after missing result: %v calls=%d sends=%d", err, calls, sends)
				}
			case "after delivery":
				if !errors.Is(err, errToolHistoryStorage) || calls != 2 || sends != 1 {
					t.Fatalf("pair replayed after delivery: %v calls=%d sends=%d", err, calls, sends)
				}
			}
		})
	}
}
