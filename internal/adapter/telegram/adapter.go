package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/chatflow"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

type Adapter struct {
	config    Config
	client    *client
	processor chatflow.Processor
	logger    *logs.Store
	stickers  *sticker.Store
	started   atomic.Bool
	ready     atomic.Bool
	mu        sync.Mutex
	cancel    context.CancelFunc
	stopped   bool
}

func NewAdapter(cfg Config, factory func(chatflow.Sender) chatflow.Processor, logger *logs.Store) *Adapter {
	a := &Adapter{config: cfg, client: newClient(cfg), logger: logger}
	if cfg.Enabled {
		a.processor = factory(a.deliver)
	}
	return a
}
func (a *Adapter) ID() string  { return "telegram" }
func (a *Adapter) Ready() bool { return a.ready.Load() }
func (a *Adapter) log(text string) {
	if a.logger != nil {
		a.logger.Warn(logs.SYSTEM, text)
	}
}

// Run is activation-owned. Construction and disabled instances never dial.
func (a *Adapter) Run(parent context.Context) {
	if !a.config.Enabled || !a.started.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		cancel()
		return
	}
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	defer a.ready.Store(false)
	backoff := time.Second
	var bot user
	for ctx.Err() == nil {
		err := a.client.call(ctx, "getMe", map[string]any{}, nil, "", &bot)
		if err == nil && bot.ID > 0 {
			break
		}
		if ctx.Err() != nil {
			return
		}
		a.log(fmt.Sprintf("Telegram 连接失败: %v", err))
		if fatal(err) || !wait(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	if ctx.Err() != nil {
		return
	}
	a.ready.Store(true)
	offset := int64(0)
	pending := make(map[int64]<-chan struct{})
	var order []int64
	advance := func() {
		for len(order) > 0 {
			id := order[0]
			select {
			case <-pending[id]:
				delete(pending, id)
				order = order[1:]
				offset = max(offset, id+1)
			default:
				return
			}
		}
	}
	backoff = time.Second
	for ctx.Err() == nil {
		advance()
		if len(order) >= 100 {
			select {
			case <-pending[order[0]]:
				continue
			case <-ctx.Done():
				return
			}
		}
		var updates []update
		err := a.client.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "limit": 100, "allowed_updates": []string{"message"}}, nil, "", &updates)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.log(fmt.Sprintf("Telegram 获取更新失败: %v", err))
			if fatal(err) {
				a.log("请检查 Telegram Token，并确认没有其他轮询进程或已配置的 Webhook")
				return
			}
			a.ready.Store(false)
			if !wait(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		a.ready.Store(true)
		backoff = time.Second
		// A pending window preserves intake ordering and permits subsequent
		// corrections/interruptions while earlier model calls are still running.
		retry := false

		for _, u := range updates {
			if ctx.Err() != nil {
				return
			}
			if u.ID < offset {
				continue
			}
			if _, exists := pending[u.ID]; exists {
				continue
			}
			if len(order) >= 100 {
				break
			}
			input, accepted, err := a.normalize(ctx, u.Message, bot)
			if err != nil {
				var apiErr *APIError
				if errors.Is(err, errMediaRejected) || (errors.As(err, &apiErr) && (apiErr.Code == 400 || apiErr.Code == 413)) {
					a.log("Telegram 跳过无法读取或超过大小限制的图片消息")
					done := make(chan struct{})
					close(done)
					pending[u.ID] = done
					order = append(order, u.ID)
					continue
				}
				if fatal(err) {
					a.log("Telegram 图片读取失败，请检查 Bot Token")
					return
				}
				a.log(fmt.Sprintf("Telegram 消息读取失败，将重试: %v", err))
				retry = true
				break
			}
			var done <-chan struct{}
			if accepted {
				done = a.processor.Submit(input)
			} else {
				closed := make(chan struct{})
				close(closed)
				done = closed
			}
			pending[u.ID] = done
			order = append(order, u.ID)
		}
		advance()
		if retry {
			if !wait(ctx, time.Second) {
				return
			}
		} else if len(order) > 0 {
			// Unconfirmed updates make long polling return immediately. Bound
			// retries without delaying a newly completed prefix unnecessarily.
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-pending[order[0]]:
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return
			}
			timer.Stop()
		}
	}
}

func fatal(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.Code == 401 || apiErr.Code == 404 || apiErr.Code == 409)
}

func (a *Adapter) Close() {
	a.mu.Lock()
	a.stopped = true
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	a.ready.Store(false)
	if a.processor != nil {
		a.processor.Close()
	}
	a.client.http.CloseIdleConnections()
}

func (a *Adapter) Send(ctx context.Context, msg core.OutgoingMessage) error {
	if !a.config.Enabled || !a.Ready() {
		return errors.New("Telegram adapter is not connected")
	}
	return a.deliver(ctx, chatflow.Delivery{Action: "send_message", Message: msg})
}

func (a *Adapter) SendAwareness(ctx context.Context, target llm.AwarenessTarget, parts []proactive.AwarenessMessage, turn *llm.SessionTurn) error {
	if !a.Ready() {
		return errors.New("Telegram adapter is not connected")
	}
	delivery := chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: target.ID, MessageType: target.Kind, Platform: "telegram"}}
	for _, p := range parts {
		delivery.Parts = append(delivery.Parts, chatflow.Part{Type: p.Type, Text: p.Text, MentionUserID: p.MentionUserID})
	}
	return turn.WithAuthority(func() error { return a.deliver(ctx, delivery) })
}
