// Package kvlite provides a typed, zero-configuration core over optional
// embedded key-value drivers. Import a driver module (for example
// extensions/rocksdb or extensions/leveldb), then select it with WithDriver.
//
// Native RocksDB support is opt-in at build time:
//
//	go build -tags rocksdb ./extensions/rocksdb/...
//
// Values are serialized through a pluggable Codec and stored in a versioned
// envelope that carries the codec name and optional per-record expiry. Every
// local directory receives a driver manifest so it cannot be reopened through
// a different engine. The core never opens a network listener; applications
// that need remote access explicitly install github.com/webong/kvlite/extensions/http
// or github.com/webong/kvlite/extensions/redis.
package kvlite
