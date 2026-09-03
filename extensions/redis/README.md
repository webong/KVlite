# KVLite Redis extension

`github.com/webong/kvlite/extensions/redis` is the opt-in Redis-compatible
RESP server for KVLite. The root `kvlite` module remains embedded-only: an
ordinary `Open` never imports or starts a network listener.

This is the linked Go implementation. Its `kvlite-module.json` uses the same
catalog contract as drivers, so a future standalone Redis executable can be
installed and discovered without compiling a host application. It will attach
to the single KVLite database owner over private local IPC; it will not open a
second copy of a RocksDB directory. See [the module contract](../../MODULES.md).

```bash
go get github.com/webong/kvlite/extensions/redis
```

```go
import (
    "log"

    "github.com/webong/kvlite"
    kvliteredis "github.com/webong/kvlite/extensions/redis"
    _ "github.com/webong/kvlite/extensions/leveldb"
)

db, err := kvlite.Open("./data", kvlite.WithDriver("leveldb"))
if err != nil {
    log.Fatal(err)
}
defer db.Close()

server, err := kvliteredis.Serve(db, kvliteredis.Options{
    ListenAddress: "127.0.0.1:6379",
    Password:      "local-secret",
})
if err != nil {
    log.Fatal(err)
}
defer server.Close()

log.Printf("Redis endpoint: %s", server.URL())
```

`Server.Close` stops only the RESP listener and active client connections; it
does not close the caller-owned DB. A Redis server may only be attached to an
embedded DB, never to a remote HTTP client handle.

The extension supports the everyday string, TTL, hash, set, list, increment,
key-discovery, and common handshake commands used by standard Redis clients.
It is a single-node protocol gateway, not a Redis cluster: replication,
Sentinel, streams, sorted sets, Lua, transactions, and TLS are outside this
milestone. Keep it on loopback or configure an external TLS/auth boundary
before exposing it beyond a trusted host.

The `kvlite serve --redis-listen ...` CLI flag links and starts this extension
for convenience. Import the package directly when an application needs only
the Redis endpoint and no HTTP server.
