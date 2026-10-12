package llm

import (
	"FrostAgent/internal/sticker"
	"encoding/json"
	"fmt"
	"sync"
)

func hasStickerImage(content string) bool {
	var payload struct {
		Messages []struct {
			Type      string `json:"type"`
			IsSticker bool   `json:"is_sticker"`
		} `json:"messages"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return false
	}
	for _, message := range payload.Messages {
		if message.Type == "image" && message.IsSticker {
			return true
		}
	}
	return false
}

const (
	ignoreEnforceStickerMarker = "<ignore_enforce_sticker />"
	ignoreEnforceStickerReply  = "你认为对话场景实在无法发送表情。你的行为被宽容，请寻找适宜的时机，不要过度依赖宽容。"
	stickerEnforcementPrompt   = "你仍未发送表情。如果你认为对话场景实在无法发送/挑选不到适宜的表情，请调用 ignore_enforce_sticker 工具以放宽约束。此内部动作请仅输出 <ignore_enforce_sticker />。"
)

// stickerActivityState is per instance. It only counts confirmed outbound
// messages; adapter send hooks and terminal send paths own that confirmation.
type stickerActivityState struct {
	mu           sync.Mutex
	level        int
	sinceSticker int
	grace        int
	pending      int
	generation   uint64
}

// StickerReservation holds one pending ordinary outbound message until its
// adapter confirms or rejects delivery.
type StickerReservation struct {
	generation uint64
	once       sync.Once
}

func (e *Engine) stickerActivitySnapshot() (interval, remaining, grace int) {
	level := sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey))
	s := &e.stickerActivity
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyLevel(level)
	interval = sticker.ActivityInterval(level)
	if interval == 0 {
		return 0, 0, 0
	}
	return interval, max(0, interval-s.sinceSticker-s.pending), s.grace
}

func (s *stickerActivityState) applyLevel(level int) {
	if s.level != level {
		s.level = level
		s.sinceSticker = 0
		s.grace = 0
		s.pending = 0
		s.generation++
	}
}

func (e *Engine) reserveBotMessage(allowPairedSticker bool) (*StickerReservation, bool) {
	s := &e.stickerActivity
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyLevel(sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey)))
	if s.level == 0 {
		return nil, true
	}
	// Leave the last slot in each interval for a sticker. A text message
	// immediately followed by send_sticker in the same batch may share it.
	if s.grace == 0 && !allowPairedSticker && s.sinceSticker+s.pending >= sticker.ActivityInterval(s.level)-1 {
		return nil, false
	}
	s.pending++
	return &StickerReservation{generation: s.generation}, true
}

// FinishBotMessage releases a pending slot and records only confirmed delivery.
func (e *Engine) FinishBotMessage(reservation *StickerReservation, delivered, isSticker bool) {
	if e == nil {
		return
	}
	finish := func() {
		s := &e.stickerActivity
		s.mu.Lock()
		defer s.mu.Unlock()
		s.applyLevel(sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey)))
		if reservation != nil && reservation.generation == s.generation {
			s.pending--
		}
		if !delivered || s.level == 0 {
			return
		}
		if isSticker {
			s.sinceSticker = 0
			s.grace = 0
		} else {
			s.sinceSticker++
			if s.grace > 0 {
				s.grace--
			}
		}
	}
	if reservation != nil {
		reservation.once.Do(finish)
	} else {
		finish()
	}
}

// RecordBotMessage is called only after a platform ACK or transport write.
func (e *Engine) RecordBotMessage(isSticker bool) {
	e.FinishBotMessage(nil, true, isSticker)
}

func (e *Engine) grantStickerGrace() {
	s := &e.stickerActivity
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyLevel(sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey)))
	if s.level > 0 {
		s.grace = 2
	}
}

func (e *Engine) stickerActivityPrompt() string {
	interval, remaining, grace := e.stickerActivitySnapshot()
	if interval == 0 {
		return ""
	}
	if grace > 0 {
		return fmt.Sprintf("表情包发送约束已放宽，还剩 %d 轮回复。", grace)
	}
	return fmt.Sprintf("你的表情包发送积极性被设定，FrostAgent 强制你在还剩 %d 轮回复之内发送一个表情。", remaining)
}

func (e *Engine) stickerNeededBeforeReply() bool {
	interval, remaining, grace := e.stickerActivitySnapshot()
	return interval > 0 && remaining <= 1 && grace == 0
}

func appendStickerForgiveness(messages []ChatMessage, iteration int) []ChatMessage {
	id := fmt.Sprintf("internal_ignore_enforce_sticker_%d", iteration)
	return append(messages,
		ChatMessage{Role: "assistant", Content: "", ToolCalls: []ToolCall{{
			ID: id, Type: "function", Function: ToolCallFunction{Name: "ignore_enforce_sticker", Arguments: "{}"},
		}}},
		ChatMessage{Role: "tool", ToolCallID: id, Content: ignoreEnforceStickerReply},
	)
}
