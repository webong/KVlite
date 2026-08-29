module github.com/webong/kvlite

go 1.23

// v1.10.6 stays within the portable C API surface used by KVLite. Later
// grocksdb releases unconditionally require RocksDB 10.9's optional slice
// APIs, even though KVLite does not use them.
require github.com/linxGnu/grocksdb v1.10.6
