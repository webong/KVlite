# KVLite HTTP extension

`github.com/webong/kvlite/extensions/http` is an opt-in server and Go client
for KVLite's language-neutral JSON/HTTP protocol. The root `kvlite` module's
ordinary `Open` path is embedded-only and never opens a network listener.

This is the linked Go implementation. Its `kvlite-module.json` uses the same
catalog contract as drivers, so a future standalone HTTP executable can be
installed and discovered without compiling a host application. It will attach
to the single KVLite database owner over private local IPC; it will not open a
second copy of a RocksDB directory. See [the module contract](../../MODULES.md).

```go
import (
    "context"

    "github.com/webong/kvlite"
    kvlitehttp "github.com/webong/kvlite/extensions/http"
    _ "github.com/webong/kvlite/extensions/leveldb"
)

db, err := kvlite.Open("./data", kvlite.WithDriver("leveldb"))
if err != nil {
    panic(err)
}
defer db.Close()

server, err := kvlitehttp.Serve(db, kvlitehttp.Options{
    ListenAddress: "127.0.0.1:8089",
    BearerToken:   "local-secret",
})
if err != nil {
    panic(err)
}
defer server.Close()

remote, err := kvlitehttp.Connect(server.URL(), kvlitehttp.ClientOptions{
    BearerToken: "local-secret",
})
if err != nil {
    panic(err)
}
defer remote.Close()

_ = remote.Put(context.Background(), "user:101", map[string]any{"name": "Ada"})
```

`Options.DriverPaths` exposes additional server-owned driver/path mappings.
Clients can select one only by name through `ClientOptions.Driver`; a request
for an unavailable or unexposed driver returns a structured error. The server
never accepts a remote filesystem path.

If the owner also starts the independent Redis extension, set
`Options.RedisURL` to that server's `URL()` so HTTP discovery advertises it for
the primary driver mapping. The standalone `kvlite serve` CLI wires this up
when `--redis-listen` is supplied. The C ABI and embedded language bindings do
not link either server extension.
