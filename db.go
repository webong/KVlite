package kvlite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DB is a concurrency-safe KVLite database handle.
type DB struct {
	engine  Engine
	cfg     config
	backend Backend

	mu sync.RWMutex
	// protocolMu serializes multi-step optional protocol commands (for example
	// a multi-record hash or list update) and generic scalar replacement, so
	// each logical key observes one coherent representation.
	protocolMu   sync.Mutex
	collectionMu sync.Mutex
	closed       bool
}

// Open opens or creates a KVLite database using a driver linked into this
// process. Import a driver extension package (for example extensions/rocksdb
// or extensions/leveldb) and select it with WithDriver.
// Prebuilt artifact discovery is exposed separately through the module catalog.
// If a runtime c-shared driver module is installed and verified, KVLite can
// open it directly without requiring the driver package in-process.
//
// RocksDB remains the compatibility default.
func Open(path string, options ...Option) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: database path is required", ErrInvalidArgument)
	}
	cfg, err := buildConfig(options)
	if err != nil {
		return nil, err
	}
	storage, backend, err := openConfiguredEngine(path, cfg)
	if err != nil {
		return nil, err
	}
	return newDB(storage, cfg, backend)
}

// OpenWithEngine creates an embedded KVLite handle over an Engine supplied by
// the caller. It is intended for optional transport extensions and custom
// in-process adapters. Unlike Open, it does not choose a storage driver or
// create a driver manifest; the returned DB takes ownership of and closes the
// supplied Engine.
func OpenWithEngine(storage Engine, backend Backend, options ...Option) (*DB, error) {
	if storage == nil {
		return nil, fmt.Errorf("%w: storage engine is required", ErrInvalidArgument)
	}
	canonicalBackend, err := ParseDriverName(string(backend))
	if err != nil {
		return nil, err
	}
	cfg, err := buildConfig(options)
	if err != nil {
		return nil, err
	}
	return newDB(storage, cfg, Backend(canonicalBackend))
}

func buildConfig(options []Option) (config, error) {
	cfg := defaultConfig()
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&cfg); err != nil {
			return config{}, err
		}
	}
	return cfg, nil
}

func newDB(storage Engine, cfg config, backend Backend) (*DB, error) {
	return &DB{engine: &guardedEngine{inner: storage}, cfg: cfg, backend: backend}, nil
}

func (db *DB) ensureOpen() error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return ErrClosed
	}
	return nil
}

// Put serializes and stores a value.
func (db *DB) Put(ctx context.Context, key string, value any, options ...PutOption) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if err := db.ensureOpen(); err != nil {
		return err
	}
	db.protocolMu.Lock()
	defer db.protocolMu.Unlock()
	// A logical key has one representation. Remove any collection records
	// before writing a scalar value through the generic embedded API.
	if _, err := db.deleteLogicalKey(ctx, key); err != nil {
		return fmt.Errorf("kvlite: put: %w", err)
	}
	return db.put(ctx, valueKey(key), value, options...)
}

// PutBytes stores an already-serialized payload without JSON re-encoding it.
// GetBytes returns this payload exactly as written, regardless of its format.
func (db *DB) PutBytes(ctx context.Context, key string, value []byte, options ...PutOption) error {
	return db.PutStoredValue(ctx, key, BytesCodec{}.Name(), value, options...)
}

// StoredValue is the serialized form of one live logical KVLite value. It is
// primarily useful to optional protocol extensions. ExpiresAt is a Unix-nano
// timestamp, or zero when the value has no expiry.
type StoredValue struct {
	Codec     string
	Payload   []byte
	ExpiresAt int64
}

// PutStoredValue stores a payload with an explicit codec name. Normal
// applications should prefer Put or PutBytes; this lower-level method lets a
// protocol extension preserve a negotiated serialized representation.
func (db *DB) PutStoredValue(ctx context.Context, key, codec string, value []byte, options ...PutOption) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if codec == "" {
		return fmt.Errorf("%w: codec is required", ErrInvalidArgument)
	}
	if err := db.ensureOpen(); err != nil {
		return err
	}
	db.protocolMu.Lock()
	defer db.protocolMu.Unlock()
	if _, err := db.deleteLogicalKey(ctx, key); err != nil {
		return fmt.Errorf("kvlite: put: %w", err)
	}
	cfg := putConfig{}
	for _, option := range options {
		if option != nil {
			if err := option(&cfg); err != nil {
				return err
			}
		}
	}
	return db.putPayload(ctx, valueKey(key), codec, value, cfg.ttl)
}

func (db *DB) put(ctx context.Context, key []byte, value any, options ...PutOption) error {
	if err := db.ensureOpen(); err != nil {
		return err
	}
	if len(key) == 0 {
		return fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	cfg := putConfig{}
	for _, option := range options {
		if option != nil {
			if err := option(&cfg); err != nil {
				return err
			}
		}
	}
	payload, err := db.cfg.codec.Marshal(value)
	if err != nil {
		return err
	}
	var expiresAt int64
	if cfg.ttl > 0 {
		expiresAt = db.cfg.now().Add(cfg.ttl).UnixNano()
	}
	encoded, err := marshalEnvelope(db.cfg.codec.Name(), payload, expiresAt)
	if err != nil {
		return err
	}
	if err := db.engine.Put(ctx, key, encoded); err != nil {
		return fmt.Errorf("kvlite: put: %w", err)
	}
	return nil
}

func (db *DB) putPayload(ctx context.Context, key []byte, codec string, payload []byte, ttl time.Duration) error {
	var expiresAt int64
	if ttl > 0 {
		expiresAt = db.cfg.now().Add(ttl).UnixNano()
	}
	encoded, err := marshalEnvelope(codec, payload, expiresAt)
	if err != nil {
		return err
	}
	if err := db.engine.Put(ctx, key, encoded); err != nil {
		return fmt.Errorf("kvlite: put: %w", err)
	}
	return nil
}

// Get decodes a stored value into target, which must be a non-nil pointer.
func (db *DB) Get(ctx context.Context, key string, target any) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	return db.get(ctx, valueKey(key), target)
}

// GetBytes returns the serialized payload for a live value.
func (db *DB) GetBytes(ctx context.Context, key string) ([]byte, error) {
	value, err := db.GetStoredValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return value.Payload, nil
}

// GetStoredValue returns the serialized payload and codec metadata for a live
// logical value. Expired values are removed lazily just as they are through
// Get and GetBytes.
func (db *DB) GetStoredValue(ctx context.Context, key string) (StoredValue, error) {
	if key == "" {
		return StoredValue{}, fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if err := db.ensureOpen(); err != nil {
		return StoredValue{}, err
	}
	storageKey := valueKey(key)
	data, found, err := db.engine.Get(ctx, storageKey)
	if err != nil {
		return StoredValue{}, fmt.Errorf("kvlite: get: %w", err)
	}
	if !found {
		return StoredValue{}, ErrNotFound
	}
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return StoredValue{}, err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		_ = db.engine.Delete(ctx, storageKey)
		return StoredValue{}, ErrNotFound
	}
	return StoredValue{
		Codec:     value.codec,
		Payload:   append([]byte(nil), value.payload...),
		ExpiresAt: value.expiresAt,
	}, nil
}

func (db *DB) get(ctx context.Context, key []byte, target any) error {
	if err := db.ensureOpen(); err != nil {
		return err
	}
	data, found, err := db.engine.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("kvlite: get: %w", err)
	}
	if !found {
		return ErrNotFound
	}
	err = db.decode(data, target)
	if errors.Is(err, ErrNotFound) {
		_ = db.engine.Delete(ctx, key)
	}
	return err
}

func (db *DB) decode(data []byte, target any) error {
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		return ErrNotFound
	}
	codec, ok := db.cfg.codecs[value.codec]
	if !ok {
		return fmt.Errorf("%w: %s", ErrCodecUnavailable, value.codec)
	}
	return codec.Unmarshal(value.payload, target)
}

// GetAs provides a typed alternative to Get.
func GetAs[T any](ctx context.Context, db *DB, key string) (T, error) {
	var result T
	err := db.Get(ctx, key, &result)
	return result, err
}

// Has reports whether a live value exists.
func (db *DB) Has(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if err := db.ensureOpen(); err != nil {
		return false, err
	}
	storageKey := valueKey(key)
	data, found, err := db.engine.Get(ctx, storageKey)
	if err != nil || !found {
		return false, err
	}
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return false, err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		_ = db.engine.Delete(ctx, storageKey)
		return false, nil
	}
	return true, nil
}

// Delete removes a key. Deleting a missing key succeeds.
func (db *DB) Delete(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if err := db.ensureOpen(); err != nil {
		return err
	}
	db.protocolMu.Lock()
	defer db.protocolMu.Unlock()
	if _, err := db.deleteLogicalKey(ctx, key); err != nil {
		return fmt.Errorf("kvlite: delete: %w", err)
	}
	return nil
}

// Backend reports the selected storage driver. A remote DB without an explicit
// WithDriver selection reports BackendRemote; an explicitly selected remote
// driver is reported by name.
func (db *DB) Backend() Backend {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.backend
}

// Close closes the selected embedded storage backend. Optional protocol
// extension servers are independently owned and must be closed by the caller.
// It is safe to call more than once.
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	db.mu.Unlock()
	return db.engine.Close()
}
