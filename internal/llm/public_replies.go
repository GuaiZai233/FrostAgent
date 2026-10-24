package llm

import (
	"FrostAgent/internal/core"
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
}

func NewPublicReplyLog(session *SessionContext, epoch uint64) *PublicReplyLog {
	return &PublicReplyLog{session: session, epoch: epoch}
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
func (p *PublicReplyLog) CommitFinal(text string, interrupted bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.session.mu.Lock()
	defer p.session.mu.Unlock()
	if p.epoch != 0 && p.session.epoch != p.epoch {
		return
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
	p.session.addMessageLocked(core.ChatMessage{Role: core.RoleAssistant, Content: text})
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
