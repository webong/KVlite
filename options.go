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
	blockCacheSize    int
	writeBufferSize   int
	maxWriteBuffers   int
	maxBackgroundJobs int
	sharing           *SharingOptions
	now               func() time.Time
}

func defaultConfig() config {
	codec := JSONCodec{}
	return config{
		codec:             codec,
		codecs:            map[string]Codec{codec.Name(): codec},
		blockCacheSize:    defaultBlockCacheSize,
		writeBufferSize:   defaultWriteBufferSize,
		maxWriteBuffers:   2,
		maxBackgroundJobs: 2,
		now:               time.Now,
	}
}

// Option customizes Open while preserving KVLite's safe defaults.
type Option func(*config) error

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

// WithMemoryBudget derives a conservative RocksDB profile from one total
// memory allowance. The budget must be at least 24 MiB.
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

// WithSharing starts an HTTP endpoint owned by the process that opens the DB.
func WithSharing(options SharingOptions) Option {
	return func(cfg *config) error {
		if options.ListenAddress == "" {
			options.ListenAddress = "127.0.0.1:0"
		}
		cfg.sharing = &options
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
