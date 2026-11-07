package llm

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"sync"
	"time"
)

// executionSequence owns private, in-memory task state. Conversation history,
// memory ownership and the sandbox namespace remain on the parent session.
type executionSequence struct {
	mu            sync.Mutex
	tail          *SessionTurn
	active        *SessionTurn
	transcript    []ChatMessage
	billing       *BillingRunState
	skills        *SessionContext
	proactive     bool
	messageIDs    map[uint64]bool
	memoryWritten bool
	skillInputs   []string
}

// ReserveSequenceTurn reserves arrival order without serializing other senders.
// Only explicit injection triggers cancel earlier work in the same lane.
func (s *SessionContext) ReserveSequenceTurn(key string, interrupt, proactive bool) *SessionTurn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch == 0 {
		s.epoch = 1
	}
	if s.sequences == nil {
		s.sequences = make(map[string]*executionSequence)
	}
	lane := s.sequences[key]
	if lane == nil {
		lane = &executionSequence{skills: &SessionContext{epoch: s.epoch}, proactive: proactive, messageIDs: make(map[uint64]bool)}
		s.sequences[key] = lane
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	t := &SessionTurn{done: make(chan struct{}), epoch: s.epoch, lane: lane, ctx: ctx}
	t.cancel = func() { t.authorityMu.Lock(); defer t.authorityMu.Unlock(); cancel() }
	if lane.tail != nil {
		t.wait = lane.tail.done
	}
	if interrupt {
		if lane.active != nil {
			lane.active.cancel()
		}
		// Pending turns still prepare and retain their inputs, but never call the model.
		for old := lane.tail; old != nil; old = old.previous {
			old.cancel()
		}
	}
	t.previous = lane.tail
	lane.tail = t
	return t
}

func (t *SessionTurn) Context() context.Context {
	if t != nil && t.ctx != nil {
		return t.ctx
	}
	return context.Background()
}

func (t *SessionTurn) Interrupted() bool {
	return t != nil && t.ctx != nil && t.ctx.Err() != nil
}

// WithAuthority admits a transport operation before revocation. Once admitted,
// it is in flight and may have side effects; never wait for I/O under this lock.
func (t *SessionTurn) WithAuthority(write func() error) error {
	if t == nil || t.ctx == nil {
		return write()
	}
	t.authorityMu.Lock()
	canceled := t.Interrupted()
	t.authorityMu.Unlock()
	if canceled {
		return context.Canceled
	}
	return write()
}

// UnreportedReceipt also covers adapter rejection before PrepareTurnMessages.
// The terminal owner must not discard an inherited billing ledger on early exit.
func (t *SessionTurn) UnreportedReceipt(engine *Engine) string {
	if t == nil || t.lane == nil || t.Interrupted() {
		return ""
	}
	t.lane.mu.Lock()
	state := t.lane.billing
	t.lane.mu.Unlock()
	if state == nil || !state.BillingActive {
		return ""
	}
	ctx, cancel := context.WithCancel(engine.Context())
	defer cancel()
	stop := context.AfterFunc(t.Context(), cancel)
	defer stop()
	if !engine.waitBilling(ctx, state) || t.Interrupted() {
		return ""
	}
	receipt, usage := state.Receipt(), state.Usage()
	if receipt.IterationsBilled == 0 && !receipt.WelcomeGranted && usage.TotalTokens == 0 {
		return ""
	}
	return billing.FormatReceipt(receipt.TotalBilledMinor, usage.PromptTokens, usage.CompletionTokens, receipt.LastBalanceMinor, receipt.WelcomeGranted)
}

func (t *SessionTurn) SkillSession() *SessionContext {
	if t == nil || t.lane == nil {
		return nil
	}
	return t.skillState
}

// PrepareTurnMessages is called after Wait, once the adapter has vetted the input.
// The request's transient background is never copied to the next attempt.
func (s *SessionContext) PrepareTurnMessages(t *SessionTurn, durable, prompt string, billing *BillingRunState) ([]ChatMessage, *BillingRunState, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t != nil && t.epoch != s.epoch {
		return nil, billing, 0
	}
	s.UpdatedAt = time.Now()
	if len(s.historySeq) != len(s.History) {
		s.historySeq = make([]uint64, len(s.History))
		for i := range s.History {
			s.nextMsgSeq++
			s.historySeq[i] = s.nextMsgSeq
		}
	}
	history := cloneMessages(s.History)
	s.nextMsgSeq++
	id := s.nextMsgSeq
	s.History = append(s.History, ChatMessage{Role: "user", Content: durable})
	s.historySeq = append(s.historySeq, id)
	if t == nil || t.lane == nil {
		return append(history, ChatMessage{Role: "user", Content: prompt}), billing, id
	}
	lane := t.lane
	lane.mu.Lock()
	defer lane.mu.Unlock()
	lane.active = t
	t.skillState = &SessionContext{epoch: s.epoch, activeSkills: lane.skills.skillSnapshot()}
	if lane.proactive {
		t.skillState.activeSkills = nil
		lane.memoryWritten = false
		lane.skillInputs = nil
	}
	lane.skills = t.skillState
	var messages []ChatMessage
	for i, msg := range history {
		if !lane.messageIDs[s.historySeq[i]] {
			messages = append(messages, msg)
		}
	}
	t.transcriptStart = 0
	for _, message := range messages {
		if message.Role != "system" {
			t.transcriptStart++
		}
	}
	if !lane.proactive {
		messages = append(messages, cloneMessages(lane.transcript)...)
	}
	messages = append(messages, ChatMessage{Role: "user", Content: durable})
	t.inputIndex = t.transcriptStart + len(lane.transcript)
	if lane.proactive {
		t.inputIndex = t.transcriptStart
	}
	t.durable = durable
	t.messageSeq = id
	lane.messageIDs[id] = true
	start := len(messages) - len(lane.transcript) - 1
	if lane.proactive {
		start = len(messages) - 1
	}
	lane.transcript = cloneMessages(messages[start:])
	messages[len(messages)-1].Content = prompt
	if lane.billing == nil {
		lane.billing = billing
	}
	return messages, lane.billing, id
}

func (t *SessionTurn) saveTranscript(messages []ChatMessage) {
	if t == nil || t.lane == nil {
		return
	}
	// System/Skills instructions are rebuilt from live state at the next injection.
	var transcript []ChatMessage
	for _, msg := range messages {
		if msg.Role != "system" {
			transcript = append(transcript, msg)
		}
	}
	if t.inputIndex < len(transcript) {
		transcript[t.inputIndex].Content = t.durable
	}
	if t.transcriptStart > len(transcript) {
		return // Keep the prepared checkpoint if context construction failed.
	}
	t.lane.mu.Lock()
	t.lane.transcript = cloneMessages(transcript[t.transcriptStart:])
	t.lane.mu.Unlock()
}

func (t *SessionTurn) finishSequence() {
	if t.lane == nil {
		return
	}
	lane := t.lane
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.active == t {
		lane.active = nil
	}
	// Only an interrupted attempt hands its task state to a successor. A queued
	// ordinary wake starts a new task after this one has delivered its receipt;
	// retaining that ledger would report completed charges a second time.
	if lane.tail == t || !t.Interrupted() {
		lane.transcript = nil
		lane.billing = nil
		lane.messageIDs = make(map[uint64]bool)
		lane.memoryWritten = false
		lane.skillInputs = nil
	}
	if lane.tail == t {
		lane.tail = nil
	}
	t.previous = nil
	t.cancel()
}

func (s *SessionContext) cancelSequencesLocked() {
	for _, lane := range s.sequences {
		lane.mu.Lock()
		if lane.active != nil {
			lane.active.cancel()
		}
		for t := lane.tail; t != nil; t = t.previous {
			t.cancel()
		}
		lane.mu.Unlock()
	}
}

// DropMessage removes only the caller's input, never another sender's tail.
func (s *SessionContext) DropMessage(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, seq := range s.historySeq {
		if seq == id {
			s.History = append(s.History[:i], s.History[i+1:]...)
			s.historySeq = append(s.historySeq[:i], s.historySeq[i+1:]...)
			return
		}
	}
}

func cloneMessages(messages []ChatMessage) []ChatMessage {
	result := append([]ChatMessage(nil), messages...)
	for i := range result {
		result[i].ToolCalls = append([]ToolCall(nil), result[i].ToolCalls...)
		if parts, ok := result[i].Content.([]core.ContentPart); ok {
			parts = append([]core.ContentPart(nil), parts...)
			for j := range parts {
				if parts[j].ImageURL != nil {
					url := *parts[j].ImageURL
					parts[j].ImageURL = &url
				}
			}
			result[i].Content = parts
		}
	}
	return result
}

func (t *SessionTurn) memoryState(written bool) bool {
	if t == nil || t.lane == nil {
		return written
	}
	t.lane.mu.Lock()
	defer t.lane.mu.Unlock()
	t.lane.memoryWritten = t.lane.memoryWritten || written
	return t.lane.memoryWritten
}

func runCanceled(ctx context.Context) bool {
	run, _ := RunContextFromContext(ctx)
	return ctx.Err() != nil || run.Turn.Interrupted()
}
