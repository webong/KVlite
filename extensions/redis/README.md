# KVLite Redis extension

`github.com/webong/kvlite/extensions/redis` is the opt-in Redis-compatible
RESP server for KVLite. The root `kvlite` module remains embedded-only: an
ordinary `Open` never imports or starts a network listener.

This is the linked Go implementation. Its `kvlite-module.json` uses the same
catalog contract as drivers, so a standalone executable form can be installed
and discovered without compiling a host application. Standalone mode is a
direct owner: the `kvlite-redis` process opens its own database directory
itself and serves exactly one protocol surface. See
[the module contract](../../MODULES.md).

To serve the *same* directory as HTTP, attach to an HTTP owner instead of
owning a directory. The owner holds the single writable copy; this process
translates RESP into the owner's record protocol and never opens the
directory:

```bash
kvlite-redis --upstream http://127.0.0.1:8089 --upstream-token "$KVLITE_TOKEN" \
  --upstream-driver leveldb --listen 127.0.0.1:6379
```

Attached multi-step commands are not atomic (each record operation crosses
the transport separately). In Go, `Serve` owns an embedded database while
`ServeRemote` serves from a remote handle such as `kvlitehttp.Connect`:

```go
remote, err := kvlitehttp.Connect("http://127.0.0.1:8089", kvlitehttp.ClientOptions{
    BearerToken: "local-secret",
    Driver:      kvlite.DriverLevelDB,
})
server, err := kvliteredis.ServeRemote(remote, kvliteredis.Options{
    ListenAddress: "127.0.0.1:6379",
})
```

There is also a standalone entrypoint that is shipped as an installable
executable module (no statically linked storage driver; it opens an installed
driver C-shared or native module at runtime):

```bash
kvlite-redis --path ./data --driver leveldb --listen 127.0.0.1:6379 --password "$KVLITE_PASSWORD"
```

The binary supports `--max-clients`, prints its listener URL on startup, and
exits with `Ctrl-C` when you stop ownership of the server.

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
