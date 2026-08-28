// Package kvlite provides a typed, zero-configuration Go API over RocksDB.
//
// Native RocksDB support is opt-in at build time:
//
//	go build -tags rocksdb ./...
//
// Values are serialized through a pluggable Codec and stored in a versioned
// envelope that carries the codec name and optional per-record expiry.
package kvlite
