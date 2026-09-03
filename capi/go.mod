module github.com/webong/kvlite/capi

go 1.23

require (
	github.com/webong/kvlite v0.1.0
	github.com/webong/kvlite/extensions/leveldb v0.1.0
	github.com/webong/kvlite/extensions/rocksdb v0.1.0
)

require (
	github.com/golang/snappy v0.0.0-20180518054509-2e65f85255db // indirect
	github.com/linxGnu/grocksdb v1.10.6 // indirect
	github.com/syndtr/goleveldb v1.0.0 // indirect
)

replace github.com/webong/kvlite => ..

replace github.com/webong/kvlite/extensions/leveldb => ../extensions/leveldb

replace github.com/webong/kvlite/extensions/rocksdb => ../extensions/rocksdb
