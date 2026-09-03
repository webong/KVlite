package kvlite

import (
	"context"
	"errors"
	"sync"
)

// Engine is the small storage contract implemented by KVLite driver modules.
// Drivers receive and return raw KVLite records; codecs, TTL semantics, and
// collections stay in the engine-neutral core.
type Engine interface {
	Get(context.Context, []byte) ([]byte, bool, error)
	Put(context.Context, []byte, []byte) error
	Delete(context.Context, []byte) error
	ScanPrefix(context.Context, []byte, func(key, value []byte) error) error
	Close() error
}

var errAtomicListPushUnsupported = errors.New("kvlite: engine does not support atomic list push")

// guardedEngine keeps Close from racing an in-flight backend call. The DB's
// higher-level closed flag provides friendlier early errors; this guard owns
// the actual storage lifetime.
type guardedEngine struct {
	mu     sync.RWMutex
	inner  Engine
	closed bool
}

func (engine *guardedEngine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return nil, false, ErrClosed
	}
	return engine.inner.Get(ctx, key)
}

func (engine *guardedEngine) Put(ctx context.Context, key, value []byte) error {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return ErrClosed
	}
	return engine.inner.Put(ctx, key, value)
}

func (engine *guardedEngine) Delete(ctx context.Context, key []byte) error {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return ErrClosed
	}
	return engine.inner.Delete(ctx, key)
}

func (engine *guardedEngine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return ErrClosed
	}
	return engine.inner.ScanPrefix(ctx, prefix, callback)
}

func (engine *guardedEngine) PushList(ctx context.Context, key []byte, items [][]byte, left bool) (int, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return 0, ErrClosed
	}
	remote, ok := engine.inner.(interface {
		PushList(context.Context, []byte, [][]byte, bool) (int, error)
	})
	if !ok {
		return 0, errAtomicListPushUnsupported
	}
	return remote.PushList(ctx, key, items, left)
}

func (engine *guardedEngine) Close() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil
	}
	engine.closed = true
	return engine.inner.Close()
}
