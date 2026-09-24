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
var errAtomicSetAddUnsupported = errors.New("kvlite: engine does not support atomic set add")
var errAtomicSetRemoveUnsupported = errors.New("kvlite: engine does not support atomic set remove")
var errAtomicHashDeleteUnsupported = errors.New("kvlite: engine does not support atomic hash delete")
var errAtomicReplaceUnsupported = errors.New("kvlite: engine does not support atomic logical replacement")

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

func (engine *guardedEngine) AddSet(ctx context.Context, name string, members []string) (int, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return 0, ErrClosed
	}
	remote, ok := engine.inner.(interface {
		AddSet(context.Context, string, []string) (int, error)
	})
	if !ok {
		return 0, errAtomicSetAddUnsupported
	}
	return remote.AddSet(ctx, name, members)
}

func (engine *guardedEngine) RemoveSet(ctx context.Context, name string, members []string) (int, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return 0, ErrClosed
	}
	remote, ok := engine.inner.(interface {
		RemoveSet(context.Context, string, []string) (int, error)
	})
	if !ok {
		return 0, errAtomicSetRemoveUnsupported
	}
	return remote.RemoveSet(ctx, name, members)
}

func (engine *guardedEngine) DeleteHashFields(ctx context.Context, name string, fields []string) (int, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return 0, ErrClosed
	}
	remote, ok := engine.inner.(interface {
		DeleteHashFields(context.Context, string, []string) (int, error)
	})
	if !ok {
		return 0, errAtomicHashDeleteUnsupported
	}
	return remote.DeleteHashFields(ctx, name, fields)
}

func (engine *guardedEngine) ReplaceLogicalValue(ctx context.Context, key string, encoded []byte) error {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return ErrClosed
	}
	remote, ok := engine.inner.(interface {
		ReplaceLogicalValue(context.Context, string, []byte) error
	})
	if !ok {
		return errAtomicReplaceUnsupported
	}
	return remote.ReplaceLogicalValue(ctx, key, encoded)
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
