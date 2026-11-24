package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func (b *BillingRunState) NextCall() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextCall
	b.nextCall++
	return id
}

func (b *BillingRunState) setBalance(balance int64, welcome bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.LastBalanceMinor = balance
	b.WelcomeGranted = b.WelcomeGranted || welcome
}

func (b *BillingRunState) beginCall() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == 0 {
		b.idle = make(chan struct{})
	}
	b.pending++
}

func (b *BillingRunState) endCall() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending--
	if b.pending == 0 {
		close(b.idle)
	}
}

// Wait delays only terminal receipt ownership, not processing injected input.
// An interrupted owner leaves all unsettled usage to its successor.
func (b *BillingRunState) Wait(ctx context.Context) bool {
	b.mu.Lock()
	idle, pending := b.idle, b.pending
	b.mu.Unlock()
	if pending == 0 {
		return ctx.Err() == nil
	}
	select {
	case <-idle:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func (b *BillingRunState) Usage() core.Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}

// Refresh only after every overlapping reservation has settled. Individual
// commit/release responses can arrive out of order and carry stale balances.
func (e *Engine) waitBilling(ctx context.Context, state *BillingRunState) bool {
	if !state.Wait(ctx) {
		return false
	}
	if e.BillingClient != nil {
		balanceCtx, cancel := context.WithTimeout(ctx, e.BillingConfig.Timeout)
		balance, err := e.BillingClient.Balance(balanceCtx, state.Platform, state.ExternalID)
		cancel()
		if err == nil && balance != nil && ctx.Err() == nil {
			state.setBalance(balance.BalanceMinor, false)
		} else if err != nil && ctx.Err() == nil {
			e.Log().Warn(logs.SYSTEM, fmt.Sprintf("结算后余额刷新失败: %v", err))
		}
	}
	return ctx.Err() == nil
}

type BillingReceipt struct {
	TotalBilledMinor int64
	LastBalanceMinor int64
	WelcomeGranted   bool
	IterationsBilled int
}

func (b *BillingRunState) Receipt() BillingReceipt {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BillingReceipt{b.TotalBilledMinor, b.LastBalanceMinor, b.WelcomeGranted, b.IterationsBilled}
}

type settledResponse struct {
	response  *core.ChatResponse
	err       error
	commitErr error
}

// chatAndSettle lets the loop abandon noncooperative providers without losing
// accounting. The worker owns reservation settlement exactly once, including
// successful responses that arrive after their attempt has been interrupted.
func (e *Engine) chatAndSettle(ctx context.Context, request core.ChatRequest, reservation string, state *BillingRunState) (*core.ChatResponse, error, error) {
	if state != nil {
		state.beginCall()
	}
	done := make(chan settledResponse, 1)
	go func() {
		if state != nil {
			defer state.endCall()
		}
		var resp *core.ChatResponse
		err := ctx.Err()
		if runCanceled(ctx) {
			err = context.Canceled
		}
		if err == nil {
			resp, err = e.Provider.Chat(ctx, request)
		}
		if err == nil && resp == nil {
			err = errors.New("provider returned no response")
		}
		var commitErr error
		if state != nil {
			if err == nil && resp.Usage != nil {
				state.mu.Lock()
				state.usage.PromptTokens += resp.Usage.PromptTokens
				state.usage.CompletionTokens += resp.Usage.CompletionTokens
				state.usage.TotalTokens += resp.Usage.TotalTokens
				state.mu.Unlock()
			}
			if reservation != "" {
				bctx, cancel := context.WithTimeout(context.WithoutCancel(e.Context()), e.BillingConfig.Timeout)
				if err != nil {
					reason := billing.ReasonModelFailed
					if ctx.Err() != nil || errors.Is(err, context.Canceled) {
						reason = billing.ReasonRequestCancelled
					}
					if released, releaseErr := e.BillingClient.ReleaseLLM(bctx, reservation, reason); releaseErr != nil {
						e.Log().Error(logs.SYSTEM, fmt.Sprintf("释放预扣款失败 (%s): %v", reservation, releaseErr))
					} else if released != nil {
						state.setBalance(released.BalanceMinor, false)
					}
				} else {
					var prompt, completion int
					if resp.Usage != nil {
						prompt, completion = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
					}
					amount := billing.CalculateMinorUnits(prompt, completion, e.BillingConfig.Price(request.Model))
					var committed *billing.LLMReservationResult
					committed, commitErr = e.BillingClient.CommitLLM(bctx, reservation, amount)
					if commitErr != nil {
						e.Log().Error(logs.SYSTEM, fmt.Sprintf("计费结算提交失败 (%s): %v", reservation, commitErr))
					} else if committed != nil {
						state.mu.Lock()
						state.TotalBilledMinor += amount
						state.LastBalanceMinor = committed.BalanceMinor
						state.IterationsBilled++
						state.mu.Unlock()
					}
				}
				cancel()
			}
		}
		done <- settledResponse{resp, err, commitErr}
	}()
	select {
	case value := <-done:
		return value.response, value.err, value.commitErr
	case <-ctx.Done():
		return nil, ctx.Err(), nil
	}
}

// Fill every unfinished call in a partial batch. Unknown side effects are not
// replayable successes or failures; the model must check their status first.
func completeInterruptedTranscript(messages []ChatMessage, started map[string]bool) []ChatMessage {
	result := cloneMessages(messages)
	last := -1
	for i := len(result) - 1; i >= 0; i-- {
		if result[i].Role == "assistant" && len(result[i].ToolCalls) > 0 {
			last = i
			break
		}
		if result[i].Role != "tool" {
			break
		}
	}
	if last < 0 {
		return result
	}
	completed := make(map[string]bool)
	for _, msg := range result[last+1:] {
		completed[msg.ToolCallID] = true
	}
	for _, call := range result[last].ToolCalls {
		if completed[call.ID] {
			continue
		}
		content := "Interrupted before execution: this tool was not started."
		if started[call.ID] {
			content = "Interrupted while executing: outcome unknown. Side effects may already have occurred. Do not repeat this operation automatically; verify its status first."
		}
		result = append(result, ChatMessage{Role: "tool", ToolCallID: call.ID, Content: content})
	}
	return result
}

func executeInterruptibleTool(ctx context.Context, tool ToolExecutor, call ToolCall) (core.ToolExecutionResult, error) {
	if runCanceled(ctx) {
		return core.ToolExecutionResult{}, context.Canceled
	}
	type response struct {
		result core.ToolExecutionResult
		err    error
	}
	done := make(chan response, 1)
	go func() {
		if runCanceled(ctx) {
			done <- response{err: context.Canceled}
			return
		}
		result, err := executeToolResult(ctx, tool, call)
		done <- response{result, err}
	}()
	select {
	case value := <-done:
		return value.result, value.err
	case <-ctx.Done():
		return core.ToolExecutionResult{}, ctx.Err()
	}
}

// Transport admission is checked by the adapter immediately before the write.
// An admitted operation may finish in the background; its callback must record
// confirmed side effects independently of the cancelled attempt's local state.
func awaitDelivery(ctx context.Context, operation func() error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		if ctx.Err() != nil {
			done <- ctx.Err()
			return
		}
		done <- operation()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *SessionTurn) AwaitDelivery(operation func()) {
	if t == nil {
		operation()
		return
	}
	_ = awaitDelivery(t.Context(), func() error { operation(); return nil })
}
