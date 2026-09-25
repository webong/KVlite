module github.com/webong/kvlite/extensions/lmdb

go 1.23

require (
	github.com/PowerDNS/lmdb-go v1.9.4
	github.com/webong/kvlite v0.1.0
)

replace github.com/webong/kvlite => ../..
