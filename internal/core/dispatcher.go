package core

import (
	"context"
	"fmt"
	"sync"
)

// DefaultDispatcher 是 MessageDispatcher 的标准实现
type DefaultDispatcher struct {
	adapters map[string]MessageAdapter
	mu       sync.RWMutex
}

func NewDefaultDispatcher() *DefaultDispatcher {
	return &DefaultDispatcher{
		adapters: make(map[string]MessageAdapter),
	}
}

// RegisterAdapter 注册一个新的平台适配器
func (d *DefaultDispatcher) RegisterAdapter(adapter MessageAdapter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.adapters[adapter.ID()] = adapter
}

// Dispatch 根据平台标识将消息分发给对应的适配器
func (d *DefaultDispatcher) Dispatch(ctx context.Context, platform string, msg OutgoingMessage) error {
	d.mu.RLock()
	adapter, ok := d.adapters[platform]
	d.mu.RUnlock()

	if !ok {
		return fmt.Errorf("未找到平台适配器: %s", platform)
	}

	return adapter.Send(ctx, msg)
}
