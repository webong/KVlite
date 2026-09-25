module github.com/webong/kvlite/extensions/boltdb

go 1.23

require (
	github.com/webong/kvlite v0.1.0
	go.etcd.io/bbolt v1.4.3
)

require golang.org/x/sys v0.29.0 // indirect

replace github.com/webong/kvlite => ../..
