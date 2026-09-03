# KVLite extensions

KVLite core is embedded by default and has no built-in storage engine or
network listener. Every optional capability lives under this directory,
including storage-engine drivers.

| Extension | Kind | Go module | Notes |
| --- | --- | --- | --- |
| [`rocksdb/`](rocksdb/) | Driver | `github.com/webong/kvlite/extensions/rocksdb` | Requires the `rocksdb` build tag and a compatible RocksDB library. |
| [`leveldb/`](leveldb/) | Driver | `github.com/webong/kvlite/extensions/leveldb` | Pure-Go LevelDB implementation. |
| [`berkeleydb/`](berkeleydb/) | Driver | `github.com/webong/kvlite/extensions/berkeleydb` | CGo-only; the application owner supplies a licensed Berkeley DB C library. |
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
listener, load an artifact, or select a local database path by itself. You can
execute an installed standalone module artifact with:

```bash
kvlite module run redis -- --path ./data --listen 127.0.0.1:6379
```

See
[the module contract](../MODULES.md).

Berkeley DB is importable only as an explicit CGo extension. It is excluded
from default bundles and requires a Berkeley DB distribution the application
owner is entitled to use.

Each implemented nested module is versioned independently. New release tags
use paths such as `extensions/leveldb/v0.1.0` and
`extensions/rocksdb/v0.1.0`.
