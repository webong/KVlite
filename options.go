package kvlite

import (
	"fmt"
	"time"
)

const (
	defaultBlockCacheSize  = 64 << 20
	defaultWriteBufferSize = 16 << 20
)

type config struct {
	codec             Codec
	codecs            map[string]Codec
	driver            DriverName
	driverExplicit    bool
	blockCacheSize    int
	writeBufferSize   int
	maxWriteBuffers   int
	maxBackgroundJobs int
	now               func() time.Time
}

func defaultConfig() config {
	codec := JSONCodec{}
	return config{
		codec:             codec,
		codecs:            map[string]Codec{codec.Name(): codec},
		driver:            DriverRocksDB,
		blockCacheSize:    defaultBlockCacheSize,
		writeBufferSize:   defaultWriteBufferSize,
		maxWriteBuffers:   2,
		maxBackgroundJobs: 2,
		now:               time.Now,
	}
}

// Option customizes Open while preserving KVLite's safe defaults.
type Option func(*config) error

// WithBackend is the backwards-compatible name for WithDriver.
//
// New applications should use WithDriver. A database directory is bound to
// the chosen driver by KVLite metadata, so use a new path (or an explicit
// export and import) when changing engines.
func WithBackend(backend Backend) Option {
	return WithDriver(string(backend))
}

// WithDriver selects the installed storage driver that owns path. The default
// remains rocksdb for compatibility, but the core does not include RocksDB or
// any other driver: import the driver package you intend to use first.
func WithDriver(driver string) Option {
	return func(cfg *config) error {
		canonical, err := normalizeDriverName(DriverName(driver))
		if err != nil {
			return err
		}
		cfg.driver = canonical
		cfg.driverExplicit = true
		return nil
	}
}

// WithCodec selects the codec used for new values. Previously registered
// codecs remain available for decoding existing values.
func WithCodec(codec Codec) Option {
	return func(cfg *config) error {
		if codec == nil || codec.Name() == "" {
			return fmt.Errorf("%w: codec and codec name are required", ErrInvalidArgument)
		}
		cfg.codec = codec
		cfg.codecs[codec.Name()] = codec
		return nil
	}
}

// WithRegisteredCodec makes a historical or alternate codec available for
// reads without selecting it for new writes.
func WithRegisteredCodec(codec Codec) Option {
	return func(cfg *config) error {
		if codec == nil || codec.Name() == "" {
			return fmt.Errorf("%w: codec and codec name are required", ErrInvalidArgument)
		}
		cfg.codecs[codec.Name()] = codec
		return nil
	}
}

// WithMemoryBudget derives a conservative profile from one total memory
// allowance. RocksDB and LevelDB both use the cache and write-buffer portions
// of this profile. The budget must be at least 24 MiB.
func WithMemoryBudget(bytes int) Option {
	return func(cfg *config) error {
		if bytes < 24<<20 {
			return fmt.Errorf("%w: memory budget must be at least 24 MiB", ErrInvalidArgument)
		}
		cfg.blockCacheSize = bytes / 2
		cfg.writeBufferSize = bytes / 8
		cfg.maxWriteBuffers = 2
		return nil
	}
}

type putConfig struct {
	ttl time.Duration
}

// PutOption customizes one write.
type PutOption func(*putConfig) error

// TTL expires a value after the supplied duration. Reads stop returning it at
// the deadline even if RocksDB has not compacted its physical bytes yet.
func TTL(duration time.Duration) PutOption {
	return func(cfg *putConfig) error {
		if duration <= 0 {
			return fmt.Errorf("%w: ttl must be positive", ErrInvalidArgument)
		}
		cfg.ttl = duration
		return nil
	}
}
