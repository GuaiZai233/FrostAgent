package llm

import (
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"strings"
	"sync"
)

// PublicReplyLog separates confirmed delivery facts from a cancellable attempt.
// Late transport confirmations may arrive after Finish; they remain public but
// cannot mutate the attempt's transcript or temporary-history bookkeeping.
type PublicReplyLog struct {
	mu      sync.Mutex
	session *SessionContext
	epoch   uint64
	closed  bool
	ids     []uint64
	texts   []string
	// Fallback history and final-callback side effects have separate ownership:
	// CommitDelivered records history but does not enqueue automatic extraction.
	fallbackText  string
	finalAccepted bool
	finalID       uint64
	finalIndex    int
}

func NewPublicReplyLog(session *SessionContext, epoch uint64) *PublicReplyLog {
	return &PublicReplyLog{session: session, epoch: epoch, finalIndex: -1}
}

// RecordFinalProgress publishes the cumulative confirmed prefix as one history
// entry and checkpoints it in the originating live turn. Both projections are
// atomic with PrepareTurnMessages, so a successor sees it once, after the
// originating input and before the successor's input.
// Late confirmations update public history only, never a successor's transcript.
func (p *PublicReplyLog) RecordFinalProgress(text string, turn *SessionTurn) {
	if p.session == nil || strings.TrimSpace(text) == "" {
		return
	}
	p.session.WithEpochCommit(p.epoch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.finalAccepted {
			return
		}
		p.session.mu.Lock()
		defer p.session.mu.Unlock()
		p.updateFinalLocked(text)
		if turn == nil || turn.lane == nil {
			return
		}
		lane := turn.lane
		lane.mu.Lock()
		defer lane.mu.Unlock()
		if lane.active != turn || !lane.messageIDs[turn.messageSeq] {
			return
		}
		message := ChatMessage{Role: "assistant", Content: text}
		if p.finalIndex < 0 {
			p.finalIndex = len(lane.transcript)
			lane.transcript = append(lane.transcript, message)
		} else {
			lane.transcript[p.finalIndex] = message
		}
		lane.messageIDs[p.finalID] = true
	})
}

// The caller holds both the reply-log and session mutexes.
func (p *PublicReplyLog) updateFinalLocked(text string) {
	for i, id := range p.session.historySeq {
		if p.finalID != 0 && id == p.finalID {
			p.session.History[i].Content = text
			return
		}
	}
	p.finalID = p.session.addMessageLocked(core.ChatMessage{Role: core.RoleAssistant, Content: text})
}

func (p *PublicReplyLog) Record(text string, compact func(live bool)) {
	if strings.TrimSpace(text) == "" {
		return
	}
	p.session.WithEpochCommit(p.epoch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		id := p.session.AddMessageAtEpoch(core.ChatMessage{Role: core.RoleAssistant, Content: text}, p.epoch)
		if id == 0 {
			return
		}
		if !p.closed {
			p.ids = append(p.ids, id)
			p.texts = append(p.texts, text)
		}
		if compact != nil {
			compact(!p.closed)
		}
	})
}

// WithEpochCommit makes public history, compaction and extraction admission one
// reset boundary. The callback must not reset the session or perform network I/O.
func (s *SessionContext) WithEpochCommit(epoch uint64, commit func()) bool {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	if epoch != 0 && s.Epoch() != epoch {
		return false
	}
	commit()
	return true
}

func (p *PublicReplyLog) Text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.texts, "\n")
}

// CommitFinal replaces provisional replies atomically so concurrent readers
// never observe both the provisional and final copies of a tool-only answer.
// True admits the final callback's compaction/extraction exactly once, even if
// CommitDelivered already archived the same text. A distinct late final remains
// a new public delivery and is appended without removing fallback history.
func (p *PublicReplyLog) CommitFinal(text string, interrupted bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finalAccepted || !p.commitFinalLocked(text, interrupted) {
		return false
	}
	p.finalAccepted = true
	return true
}

// CommitDelivered finalizes confirmed tool-only output on every proactive exit,
// including silence and errors. A previously committed final answer wins; an
// old epoch must never restore history after a session reset.
func (p *PublicReplyLog) CommitDelivered() {
	p.session.WithEpochCommit(p.epoch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if !p.closed && p.finalID == 0 && len(p.texts) > 0 {
			text := strings.Join(p.texts, "\n")
			if p.commitFinalLocked(text, false) {
				p.fallbackText = text
			}
		}
	})
}

func (p *PublicReplyLog) commitFinalLocked(text string, interrupted bool) bool {
	p.session.mu.Lock()
	defer p.session.mu.Unlock()
	if p.epoch != 0 && p.session.epoch != p.epoch {
		return false
	}
	if p.fallbackText != "" && p.fallbackText == text {
		return true
	}
	if !interrupted {
		for _, id := range p.ids {
			for i, seq := range p.session.historySeq {
				if seq == id {
					p.session.History = append(p.session.History[:i], p.session.History[i+1:]...)
					p.session.historySeq = append(p.session.historySeq[:i], p.session.historySeq[i+1:]...)
					break
				}
			}
		}
	}
	p.ids = nil
	p.closed = true
	if p.finalID != 0 {
		p.updateFinalLocked(text)
	} else {
		p.session.addMessageLocked(core.ChatMessage{Role: core.RoleAssistant, Content: text})
	}
	return true
}

// Completed attempts preserve the existing final-answer history policy.
// Interrupted attempts retain already delivered replies as shared facts.
func (p *PublicReplyLog) Finish(interrupted bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	if !interrupted {
		for _, id := range p.ids {
			p.session.DropMessage(id)
		}
	}
}
