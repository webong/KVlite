package kvlite

import "errors"

var (
	ErrNotFound         = errors.New("kvlite: key not found")
	ErrClosed           = errors.New("kvlite: database is closed")
	ErrInvalidArgument  = errors.New("kvlite: invalid argument")
	ErrCodecUnavailable = errors.New("kvlite: value codec is unavailable")
	ErrRocksDBNotBuilt  = errors.New("kvlite: RocksDB support is not built; rebuild with -tags rocksdb")
)
