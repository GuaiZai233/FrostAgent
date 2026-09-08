// Package runtimescope owns the work admitted during one instance activation.
package runtimescope

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

type Scope struct {
	Config  *instanceconfig.Store
	Global  *instanceconfig.Store
	Logger  *logs.Store
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	stopped bool
	wg      sync.WaitGroup
}

func New(config, global *instanceconfig.Store, logger *logs.Store) *Scope {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scope{Config: config, Global: global, Logger: logger, ctx: ctx, cancel: cancel}
}
func (s *Scope) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return WithContext(s.ctx, s)
}
func (s *Scope) Getenv(k string) string {
	if s == nil {
		return os.Getenv(k)
	}
	if instanceconfig.GlobalKeys[k] {
		if s.Global == nil {
			return ""
		}
		return s.Global.Get(k)
	}
	if s.Config == nil {
		return ""
	}
	return s.Config.Get(k)
}
func (s *Scope) Log() *logs.Store {
	if s == nil || s.Logger == nil {
		return logs.General
	}
	return s.Logger
}

// Enter must happen before starting work; cancellation prevents any future admission.
func (s *Scope) Enter() (func(), bool) {
	if s == nil {
		return func() {}, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return func() {}, false
	}
	s.wg.Add(1)
	return s.wg.Done, true
}
func (s *Scope) Go(fn func()) bool {
	done, ok := s.Enter()
	if !ok {
		return false
	}
	go func() { defer done(); fn() }()
	return true
}
func (s *Scope) Cancel() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	s.cancel()
	s.mu.Unlock()
}
func (s *Scope) Wait() {
	if s != nil {
		s.wg.Wait()
	}
}

func First(scopes []*Scope) *Scope {
	if len(scopes) > 0 {
		return scopes[0]
	}
	return nil
}

type contextKey struct{}

func WithContext(ctx context.Context, s *Scope) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}
func FromContext(ctx context.Context) *Scope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(contextKey{}).(*Scope)
	return s
}

func (s *Scope) LookupEnv(k string) (string, bool) {
	if s == nil {
		return os.LookupEnv(k)
	}
	v, ok := s.Config.Snapshot()[k]
	if instanceconfig.GlobalKeys[k] {
		v, ok = s.Global.Snapshot()[k]
	}
	return v, ok
}
func (s *Scope) CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, v := range strings.Split(s.Getenv("WS_ALLOWED_ORIGINS"), ",") {
		if strings.TrimSpace(v) == origin {
			return true
		}
	}
	return false
}
