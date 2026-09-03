# KVLite extensions

KVLite core is embedded by default and has no built-in storage engine or
network listener. Every optional capability lives under this directory,
including storage-engine drivers.

| Extension | Kind | Go module | Notes |
| --- | --- | --- | --- |
| [`rocksdb/`](rocksdb/) | Driver | `github.com/webong/kvlite/extensions/rocksdb` | Requires the `rocksdb` build tag and a compatible RocksDB library. |
| [`leveldb/`](leveldb/) | Driver | `github.com/webong/kvlite/extensions/leveldb` | Pure-Go LevelDB implementation. |
| [`berkeleydb/`](berkeleydb/) | Driver descriptor | Not published | Reserved separately licensed driver; no implementation or artifact is supplied yet. |
| [`http/`](http/) | Transport | `github.com/webong/kvlite/extensions/http` | Explicit JSON/HTTP owner and client extension. |
| [`redis/`](redis/) | Transport | `github.com/webong/kvlite/extensions/redis` | Explicit Redis RESP2 server extension. |

For an embedded LevelDB database, choose and link only the extension you need:

```go
import (
    "github.com/webong/kvlite"
    _ "github.com/webong/kvlite/extensions/leveldb"
)

db, err := kvlite.Open("./data", kvlite.WithDriver("leveldb"))
```

Each extension publishes the same `kvlite-module.json` contract used by
standalone native bundles. The metadata describes a module but does not start a
listener, load an artifact, or select a local database path. See
[the module contract](../MODULES.md).

There is intentionally no importable Berkeley DB package until an
implementation, distribution, and license policy have been published.

Each implemented nested module is versioned independently. New release tags
use paths such as `extensions/leveldb/v0.1.0` and
`extensions/rocksdb/v0.1.0`.
