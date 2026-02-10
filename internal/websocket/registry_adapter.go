package websocket

import (
	"sync"
)

// SimpleRegistry is a basic in-memory handler registry for testing.
type SimpleRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewSimpleRegistry creates a new simple registry.
func NewSimpleRegistry() *SimpleRegistry {
	return &SimpleRegistry{
		handlers: make(map[string]Handler),
	}
}

// Register adds a handler to the registry.
func (r *SimpleRegistry) Register(method string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[method] = handler
}

// GetHandler retrieves a handler by method name.
func (r *SimpleRegistry) GetHandler(method string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[method]
	return h, ok
}

// DaemonHandlerAdapter adapts a daemon.Server to the HandlerRegistry interface.
type DaemonHandlerAdapter struct {
	getHandler func(string) (Handler, bool)
}

// NewDaemonHandlerAdapter creates an adapter for daemon.Server handlers.
func NewDaemonHandlerAdapter(getHandler func(string) (Handler, bool)) *DaemonHandlerAdapter {
	return &DaemonHandlerAdapter{
		getHandler: getHandler,
	}
}

// GetHandler retrieves a handler by method name.
func (a *DaemonHandlerAdapter) GetHandler(method string) (Handler, bool) {
	return a.getHandler(method)
}
