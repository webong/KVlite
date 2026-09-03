module github.com/webong/kvlite/examples/basic

go 1.23

require (
	github.com/webong/kvlite v0.1.0
	github.com/webong/kvlite/drivers/rocksdb v0.1.0
)

require github.com/linxGnu/grocksdb v1.10.6 // indirect

replace github.com/webong/kvlite => ../..

replace github.com/webong/kvlite/drivers/rocksdb => ../../drivers/rocksdb
