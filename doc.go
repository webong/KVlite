// Package kvlite provides a typed, zero-configuration core over optional
// embedded key-value drivers. Import a driver module (for example
// drivers/rocksdb or drivers/leveldb), then select it with WithDriver.
//
// Native RocksDB support is opt-in at build time:
//
//
//	go build -tags rocksdb ./drivers/rocksdb/...
//
// Values are serialized through a pluggable Codec and stored in a versioned
// envelope that carries the codec name and optional per-record expiry. Every
// local directory receives a driver manifest so it cannot be reopened through
// a different engine. An owner can additionally expose JSON/HTTP (with
// server-owned driver mappings) or a Redis RESP2-compatible endpoint for
// clients written in other languages.
package kvlite
