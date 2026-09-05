module github.com/webong/kvlite/extensions/redis

go 1.23

require (
	github.com/webong/kvlite v0.1.0
	github.com/webong/kvlite/extensions/http v0.1.0
)

replace github.com/webong/kvlite => ../..

replace github.com/webong/kvlite/extensions/http => ../http
