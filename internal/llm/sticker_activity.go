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

// stickerActivityState counts selected incoming turns per instance. Confirmed
// outbound messages only reset the sticker counter or consume existing grace.
type stickerActivityState struct {
	mu           sync.Mutex
	level        int
	mode         string
	sinceSticker int
	grace        int
}

func (e *Engine) stickerActivitySnapshot() (interval, remaining, grace int) {
	s := &e.stickerActivity
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyConfig(sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey)), sticker.ActivityMode(e.Getenv(sticker.ActivityModeEnvKey)))
	interval = sticker.ActivityInterval(s.level)
	if interval == 0 {
		return 0, 0, 0
	}
	return interval, max(0, interval-s.sinceSticker), s.grace
}

func (s *stickerActivityState) applyConfig(level int, mode string) {
	if s.level != level || s.mode != mode {
		s.level = level
		s.mode = mode
		s.sinceSticker = 0
		s.grace = 0
	}
}

// RecordIncomingMessage counts every real incoming chat message in all-messages
// mode, including messages that do not wake the bot. Protocol events are excluded
// by the adapters. Counting does not itself trigger a reply.
func (e *Engine) RecordIncomingMessage() {
	e.recordStickerTurn(sticker.ActivityAllMessages)
}

// RecordBotTurn counts one triggered dialogue, including proactive dialogues
// that later choose silence. Tool iterations and outbound messages are not turns.
func (e *Engine) RecordBotTurn() {
	e.recordStickerTurn(sticker.ActivityBotTurns)
}

func (e *Engine) recordStickerTurn(mode string) {
	if e == nil {
		return
	}
	s := &e.stickerActivity
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyConfig(sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey)), sticker.ActivityMode(e.Getenv(sticker.ActivityModeEnvKey)))
	interval := sticker.ActivityInterval(s.level)
	if interval > 0 && s.mode == mode {
		s.sinceSticker = min(interval, s.sinceSticker+1)
	}
}

// RecordBotMessage is called only after a platform ACK or transport write.
// Ordinary outbound messages do not advance the activity counter.
func (e *Engine) RecordBotMessage(isSticker bool) {
	if e == nil {
		return
	}
	s := &e.stickerActivity
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyConfig(sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey)), sticker.ActivityMode(e.Getenv(sticker.ActivityModeEnvKey)))
	if s.level == 0 {
		return
	}
	if isSticker {
		s.sinceSticker = 0
		s.grace = 0
	} else if s.grace > 0 {
		s.grace--
	}
}

func (e *Engine) grantStickerGrace() {
	s := &e.stickerActivity
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyConfig(sticker.ActivityLevel(e.Getenv(sticker.ActivityEnvKey)), sticker.ActivityMode(e.Getenv(sticker.ActivityModeEnvKey)))
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
	unit := "个 Bot 对话轮次"
	if sticker.ActivityMode(e.Getenv(sticker.ActivityModeEnvKey)) == sticker.ActivityAllMessages {
		unit = "条传入消息"
	}
	return fmt.Sprintf("你的表情包发送积极性被设定，FrostAgent 强制你在还剩 %d %s之内发送一个表情。", remaining, unit)
}

func (e *Engine) stickerNeededBeforeReply() bool {
	interval, remaining, grace := e.stickerActivitySnapshot()
	return interval > 0 && remaining == 0 && grace == 0
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
