package instance

import (
	"context"
	"sync"
)

// settingsWriteLock also admits cancellable runtime writers. A runtime stopped
// while a management draft holds the lock must be able to finish shutdown.
type settingsWriteLock struct {
	once sync.Once
	held chan struct{}
}

func (m *settingsWriteLock) init() { m.once.Do(func() { m.held = make(chan struct{}, 1) }) }

func (m *settingsWriteLock) LockContext(ctx context.Context) error {
	m.init()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.held <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *settingsWriteLock) Lock()   { _ = m.LockContext(context.Background()) }
func (m *settingsWriteLock) Unlock() { <-m.held }
func (m *settingsWriteLock) TryLock() bool {
	m.init()
	select {
	case m.held <- struct{}{}:
		return true
	default:
		return false
	}
}
