module github.com/webong/kvlite/capi

go 1.23.0

require (
	github.com/webong/kvlite v0.1.0
	github.com/webong/kvlite/extensions/badgerdb v0.1.0
	github.com/webong/kvlite/extensions/berkeleydb v0.1.0
	github.com/webong/kvlite/extensions/boltdb v0.1.0
	github.com/webong/kvlite/extensions/leveldb v0.1.0
	github.com/webong/kvlite/extensions/lmdb v0.1.0
	github.com/webong/kvlite/extensions/rocksdb v0.1.0
)

require (
	github.com/PowerDNS/lmdb-go v1.9.4 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgraph-io/badger/v4 v4.8.0 // indirect
	github.com/dgraph-io/ristretto/v2 v2.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang/snappy v0.0.0-20180518054509-2e65f85255db // indirect
	github.com/google/flatbuffers v25.2.10+incompatible // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/linxGnu/grocksdb v1.10.6 // indirect
	github.com/syndtr/goleveldb v1.0.0 // indirect
	go.etcd.io/bbolt v1.4.3 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.37.0 // indirect
	go.opentelemetry.io/otel/metric v1.37.0 // indirect
	go.opentelemetry.io/otel/trace v1.37.0 // indirect
	golang.org/x/net v0.41.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)

replace github.com/webong/kvlite => ..

replace github.com/webong/kvlite/extensions/badgerdb => ../extensions/badgerdb

replace github.com/webong/kvlite/extensions/berkeleydb => ../extensions/berkeleydb

replace github.com/webong/kvlite/extensions/boltdb => ../extensions/boltdb

replace github.com/webong/kvlite/extensions/leveldb => ../extensions/leveldb

replace github.com/webong/kvlite/extensions/lmdb => ../extensions/lmdb

replace github.com/webong/kvlite/extensions/rocksdb => ../extensions/rocksdb
