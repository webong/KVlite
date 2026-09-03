module github.com/webong/kvlite/extensions/leveldb

go 1.23

require (
	github.com/syndtr/goleveldb v1.0.0
	github.com/webong/kvlite v0.1.0
)

require github.com/golang/snappy v0.0.0-20180518054509-2e65f85255db // indirect

replace github.com/webong/kvlite => ../..
