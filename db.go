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
	// redisMu serializes multi-step Redis commands (for example HSET and
	// LPUSH) so each command observes one coherent view of the database.
	redisMu      sync.Mutex
	collectionMu sync.Mutex
	closed       bool
	server       *shareServer
	redis        *redisServer
}

// Open opens or creates a KVLite database using an installed storage driver.
// Import a driver package (for example drivers/rocksdb or drivers/leveldb)
// and select it with WithDriver. RocksDB remains the compatibility default.
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
	db := &DB{engine: &guardedEngine{inner: storage}, cfg: cfg, backend: backend}
	if cfg.sharing != nil {
		server, err := startShareServer(db, *cfg.sharing)
		if err != nil {
			_ = db.engine.Close()
			return nil, err
		}
		db.server = server
	}
	if cfg.redis != nil {
		redis, err := startRedisServer(db, *cfg.redis)
		if err != nil {
			if db.server != nil {
				_ = db.server.close()
			}
			_ = db.engine.Close()
			return nil, err
		}
		db.redis = redis
	}
	return db, nil
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
	db.redisMu.Lock()
	defer db.redisMu.Unlock()
	// A logical key has one Redis-compatible type. Remove any collection
	// records before writing a scalar value through the generic API.
	if _, err := db.redisDeleteRaw(ctx, key); err != nil {
		return fmt.Errorf("kvlite: put: %w", err)
	}
	return db.put(ctx, valueKey(key), value, options...)
}

// PutBytes stores an already-serialized payload without JSON re-encoding it.
// GetBytes returns this payload exactly as written, regardless of its format.
func (db *DB) PutBytes(ctx context.Context, key string, value []byte, options ...PutOption) error {
	if key == "" {
		return fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if err := db.ensureOpen(); err != nil {
		return err
	}
	db.redisMu.Lock()
	defer db.redisMu.Unlock()
	if _, err := db.redisDeleteRaw(ctx, key); err != nil {
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
	return db.putPayload(ctx, valueKey(key), BytesCodec{}.Name(), value, cfg.ttl)
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
	if key == "" {
		return nil, fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if err := db.ensureOpen(); err != nil {
		return nil, err
	}
	storageKey := valueKey(key)
	data, found, err := db.engine.Get(ctx, storageKey)
	if err != nil {
		return nil, fmt.Errorf("kvlite: get: %w", err)
	}
	if !found {
		return nil, ErrNotFound
	}
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		_ = db.engine.Delete(ctx, storageKey)
		return nil, ErrNotFound
	}
	return append([]byte(nil), value.payload...), nil
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
	db.redisMu.Lock()
	defer db.redisMu.Unlock()
	if _, err := db.redisDeleteRaw(ctx, key); err != nil {
		return fmt.Errorf("kvlite: delete: %w", err)
	}
	return nil
}

// SharingAddress returns the bound HTTP address, or an empty string when the
// sharing layer is disabled.
func (db *DB) SharingAddress() string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.server == nil {
		return ""
	}
	return db.server.address()
}

// RedisAddress returns the bound Redis RESP address, or an empty string when
// the Redis compatibility endpoint is disabled.
func (db *DB) RedisAddress() string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.redis == nil {
		return ""
	}
	return db.redis.address()
}

// Backend reports the selected storage driver. A remote DB without an explicit
// WithDriver selection reports BackendRemote; an explicitly selected remote
// driver is reported by name.
func (db *DB) Backend() Backend {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.backend
}

// Close stops sharing and closes the selected storage backend. It is safe to
// call more than once.
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	server := db.server
	db.server = nil
	redis := db.redis
	db.redis = nil
	db.mu.Unlock()

	if server != nil {
		_ = server.close()
	}
	if redis != nil {
		_ = redis.close()
	}
	return db.engine.Close()
}
