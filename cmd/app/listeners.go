package main

import (
	"FrostAgent/internal/logs"
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

type handlerProxy struct {
	mu      sync.RWMutex
	handler http.Handler
}

func (p *handlerProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	handler := p.handler
	p.mu.RUnlock()
	handler.ServeHTTP(w, r)
}

func (p *handlerProxy) Set(handler http.Handler) {
	p.mu.Lock()
	p.handler = handler
	p.mu.Unlock()
}

type activeListener struct {
	listener net.Listener
	server   *http.Server
	proxy    *handlerProxy
}

type listenerSet struct {
	active map[string]*activeListener
	errors chan error
}

func newListenerSet() *listenerSet {
	return &listenerSet{active: make(map[string]*activeListener), errors: make(chan error, 8)}
}

func (s *listenerSet) start(address string, listener net.Listener, handler http.Handler) *activeListener {
	proxy := &handlerProxy{handler: handler}
	server := &http.Server{Addr: address, Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	item := &activeListener{listener: listener, server: server, proxy: proxy}
	go func() {
		logs.General.Listening(address)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case s.errors <- err:
			default:
			}
		}
	}()
	return item
}

func samePort(a, b string) bool {
	_, first, errA := net.SplitHostPort(a)
	_, second, errB := net.SplitHostPort(b)
	return errA == nil && errB == nil && first == second
}

// Apply binds new addresses before retiring old ones. A host change on the
// same port briefly releases the old listener, then restores it on bind error.
func (s *listenerSet) Apply(desired map[string]http.Handler) error {
	staged := make(map[string]net.Listener)
	released := make(map[string]http.Handler)
	rollback := func() {
		for _, listener := range staged {
			_ = listener.Close()
		}
		for address, handler := range released {
			listener, err := net.Listen("tcp", address)
			if err != nil {
				select {
				case s.errors <- err:
				default:
				}
				continue
			}
			s.active[address] = s.start(address, listener, handler)
		}
	}
	for address := range desired {
		if s.active[address] != nil {
			continue
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			for oldAddress, old := range s.active {
				if oldAddress != address && samePort(oldAddress, address) {
					old.proxy.mu.RLock()
					released[oldAddress] = old.proxy.handler
					old.proxy.mu.RUnlock()
					_ = old.server.Close()
					delete(s.active, oldAddress)
				}
			}
			listener, err = net.Listen("tcp", address)
		}
		if err != nil {
			rollback()
			return err
		}
		staged[address] = listener
	}
	for address, handler := range desired {
		if current := s.active[address]; current != nil {
			current.proxy.Set(handler)
		} else {
			s.active[address] = s.start(address, staged[address], handler)
		}
	}
	for address, current := range s.active {
		if _, keep := desired[address]; !keep {
			_ = current.server.Close()
			delete(s.active, address)
		}
	}
	return nil
}

func (s *listenerSet) Close(ctx context.Context) {
	for address, current := range s.active {
		_ = current.server.Shutdown(ctx)
		delete(s.active, address)
	}
}
