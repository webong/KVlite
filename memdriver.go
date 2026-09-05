package kvlite

import (
	"bytes"
	"context"
	"sort"
	"sync"
)

// DriverMemory is the stable name of the built-in ephemeral driver. It is
// selected explicitly with WithDriver("memory"): core never silently stores
// real data in it when another driver is available (see DefaultDriver).
const DriverMemory DriverName = "memory"

// The memory driver is part of core, not an extension: a zero-dependency,
// array-backed engine for trying out the API, running tests, and serving
// throwaway data. Every Open gets a fresh, empty store; everything is lost
// when the last handle closes. The on-disk manifest still records the driver
// choice, so a directory initialized for another engine is rejected rather
// than silently adopted — but reopening a memory directory yields an empty
// database, never old records.
func init() {
	MustRegisterDriver(memoryDriver{})
	MustRegisterLinkedModule(ModuleManifest{
		SchemaVersion: ModuleManifestVersion,
		Name:          string(DriverMemory),
		Kind:          ModuleKindDriver,
		Version:       "v0.1.0",
		ModuleABI:     ModuleABIVersion,
		Driver:        DriverMemory,
		Capabilities:  []string{"embedded-storage", "ephemeral"},
		License:       "Apache-2.0",
	})
}

type memoryDriver struct{}

func (memoryDriver) Info() DriverInfo {
	return DriverInfo{
		Driver:         DriverMemory,
		Backend:        Backend(DriverMemory),
		Implementation: "memory",
		Format:         "memory-v1",
		Version:        "v1.0.0",
	}
}

func (memoryDriver) Available() error { return nil }

func (memoryDriver) Open(path string, options DriverOptions) (Engine, error) {
	_ = path
	_ = options
	return &memoryDriverEngine{data: make(map[string][]byte)}, nil
}

type memoryDriverEngine struct {
	mu     sync.RWMutex
	data   map[string][]byte
	closed bool
}

func (engine *memoryDriverEngine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.closed {
		return nil, false, ErrClosed
	}
	value, found := engine.data[string(key)]
	return append([]byte(nil), value...), found, nil
}

func (engine *memoryDriverEngine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return ErrClosed
	}
	engine.data[string(key)] = append([]byte(nil), value...)
	return nil
}

func (engine *memoryDriverEngine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return ErrClosed
	}
	delete(engine.data, string(key))
	return nil
}

func (engine *memoryDriverEngine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	engine.mu.RLock()
	if engine.closed {
		engine.mu.RUnlock()
		return ErrClosed
	}
	keys := make([]string, 0)
	for key := range engine.data {
		if bytes.HasPrefix([]byte(key), prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	items := make([][2][]byte, 0, len(keys))
	for _, key := range keys {
		items = append(items, [2][]byte{[]byte(key), append([]byte(nil), engine.data[key]...)})
	}
	engine.mu.RUnlock()
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := callback(item[0], item[1]); err != nil {
			return err
		}
	}
	return nil
}

func (engine *memoryDriverEngine) Close() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil
	}
	engine.closed = true
	// Dropping the map releases every record at once; there is nothing to
	// flush because an ephemeral store is never the system of record.
	engine.data = make(map[string][]byte)
	return nil
}
