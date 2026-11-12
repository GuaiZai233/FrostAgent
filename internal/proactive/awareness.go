package proactive

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	AwarenessEnabled                 = "ENABLE_PROACTIVE_AWARENESS"
	AwarenessInterval                = "PROACTIVE_AWARENESS_INTERVAL"
	AwarenessPrivateWhitelist        = "PROACTIVE_AWARENESS_PRIVATE_WHITELIST"
	AwarenessGroupWhitelist          = "PROACTIVE_AWARENESS_GROUP_WHITELIST"
	AwarenessPrivateWhitelistEnabled = "ENABLE_PROACTIVE_AWARENESS_PRIVATE_WHITELIST"
	AwarenessGroupWhitelistEnabled   = "ENABLE_PROACTIVE_AWARENESS_GROUP_WHITELIST"
	DefaultAwarenessInterval         = "30m - 2h"
	AwarenessPrivatePrompt           = "此为触发主动陪伴逻辑的系统提示。此触发不关联任何一条具体消息。你需要结合过往的聊天历史，主动询问或关心用户。如果正在进行对话请补充你的想法或继续参与讨论，不要转移话题；如果距离上一次对话较久，可以开启新话题。"
	AwarenessGroupPrompt             = "此为触发主动陪伴逻辑的系统提示。此触发不关联任何一条具体消息。你需要结合过往的聊天历史，在群里发送水群内容，可以具体到某一位群友（艾特请调用send_message工具）也可以根据总compact内容延续或启动新话题。如果群内正在进行对话请参与讨论。"
)

var awarenessDuration = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?[mh]$`)

func ParseAwarenessInterval(raw string) (time.Duration, time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		raw = DefaultAwarenessInterval
	}
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("活跃阈值应为下限 - 上限，例如 30m - 2h")
	}
	var bounds [2]time.Duration
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if !awarenessDuration.MatchString(part) {
			return 0, 0, fmt.Errorf("活跃阈值必须使用 m 或 h 结尾的正数时长")
		}
		d, err := time.ParseDuration(part)
		if err != nil || d <= 0 {
			return 0, 0, fmt.Errorf("活跃阈值时长无效")
		}
		bounds[i] = d
	}
	if bounds[0] > bounds[1] {
		return 0, 0, fmt.Errorf("活跃阈值下限不能大于上限")
	}
	return bounds[0], bounds[1], nil
}

func AwarenessAllowed(getenv func(string) string, platform, kind, id string) bool {
	if getenv == nil || !strings.EqualFold(strings.TrimSpace(getenv(AwarenessEnabled)), "true") || id == "" {
		return false
	}
	if _, _, err := ParseAwarenessInterval(getenv(AwarenessInterval)); err != nil {
		return false
	}
	switch kind {
	case "group":
		return IsGroupAllowedWith(getenv, AwarenessGroupWhitelistEnabled, AwarenessGroupWhitelist, id, platform)
	case "private":
		return IsGroupAllowedWith(getenv, AwarenessPrivateWhitelistEnabled, AwarenessPrivateWhitelist, id, platform)
	default:
		return false
	}
}

// AwarenessMessage is deliberately limited to current-conversation text and mentions.
type AwarenessMessage struct {
	Type          string `json:"type"`
	Text          string `json:"text,omitempty"`
	MentionUserID string `json:"mention_user_id,omitempty"`
}

func ParseAwarenessMessages(args string) ([]AwarenessMessage, error) {
	var payload struct {
		Session  string             `json:"session"`
		Messages []AwarenessMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil || payload.Session != "" || len(payload.Messages) == 0 {
		return nil, fmt.Errorf("主动感知仅允许向当前会话发送文本或艾特")
	}
	text := false
	for _, m := range payload.Messages {
		switch m.Type {
		case "plain":
			if strings.TrimSpace(m.Text) == "" {
				return nil, fmt.Errorf("主动感知文本不能为空")
			}
			text = true
		case "mention_user":
			if strings.TrimSpace(m.MentionUserID) == "" {
				return nil, fmt.Errorf("主动感知艾特目标不能为空")
			}
		default:
			return nil, fmt.Errorf("主动感知不允许消息类型 %q", m.Type)
		}
	}
	if !text {
		return nil, fmt.Errorf("主动感知消息必须包含文本")
	}
	return payload.Messages, nil
}

func AwarenessSendAllowed(args string) bool {
	_, err := ParseAwarenessMessages(args)
	return err == nil
}

func AwarenessParameters() map[string]any {
	return map[string]any{"type": "object", "required": []string{"messages"}, "additionalProperties": false,
		"properties": map[string]any{"messages": map[string]any{"type": "array", "minItems": 1,
			"items": map[string]any{"type": "object", "required": []string{"type"}, "additionalProperties": false,
				"properties": map[string]any{"type": map[string]any{"type": "string", "enum": []string{"plain", "mention_user"}},
					"text": map[string]any{"type": "string"}, "mention_user_id": map[string]any{"type": "string"}}}}}}
}

type awarenessDeadline struct {
	due time.Time
}

// AwarenessClock keeps independent deadlines. Observing ordinary messages never
// resets them. Callers supply only currently allowed, routable conversations.
type AwarenessClock struct {
	mu        sync.Mutex
	deadlines map[string]awarenessDeadline
	running   map[string]bool
	interval  string
	Random    func() float64
}

func (c *AwarenessClock) Due(now time.Time, interval string, targets []string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lo, hi, err := ParseAwarenessInterval(interval)
	if err != nil {
		c.deadlines = nil
		return nil
	}
	if c.deadlines == nil || c.interval != interval {
		c.deadlines = make(map[string]awarenessDeadline)
		c.interval = interval
	}
	if c.running == nil {
		c.running = make(map[string]bool)
	}
	seen := make(map[string]bool)
	var due []string
	for _, id := range targets {
		if seen[id] {
			continue
		}
		seen[id] = true
		d, exists := c.deadlines[id]
		trigger := exists && !now.Before(d.due) && !c.running[id]
		if trigger {
			due = append(due, id)
			c.running[id] = true
		}
		if !exists || trigger {
			random := c.Random
			if random == nil {
				random = rand.Float64
			}
			d.due = now.Add(lo + time.Duration(random()*float64(hi-lo)))
		}
		c.deadlines[id] = d
	}
	for id := range c.deadlines {
		if !seen[id] {
			delete(c.deadlines, id)
		}
	}
	return due
}

func (c *AwarenessClock) NextDelay(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	delay := time.Second
	for id, d := range c.deadlines {
		if c.running[id] {
			continue
		}
		if left := d.due.Sub(now); left < delay {
			delay = max(left, time.Millisecond)
		}
	}
	return delay
}

func (c *AwarenessClock) Done(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.running, id)
}
