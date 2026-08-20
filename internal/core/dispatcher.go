// 负责把 core 层产出的 OutgoingMessage 按平台标识准确送到对应的 MessageAdapter

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

// UnregisterAdapter 移除平台适配器
func (d *DefaultDispatcher) UnregisterAdapter(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.adapters, id)
}

// GetAdapter 获取指定平台适配器
func (d *DefaultDispatcher) GetAdapter(id string) (MessageAdapter, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	adapter, ok := d.adapters[id]
	return adapter, ok
}

// ListAdapters 列出所有已注册的适配器 ID
func (d *DefaultDispatcher) ListAdapters() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ids := make([]string, 0, len(d.adapters))
	for id := range d.adapters {
		ids = append(ids, id)
	}
	return ids
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
