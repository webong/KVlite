# KVLite Go drivers

KVLite core deliberately contains no database engine. Install one driver module
and register it with a normal blank import:

```go
import (
    "github.com/webong/kvlite"
    _ "github.com/webong/kvlite/drivers/leveldb"
)

db, err := kvlite.Open("./data", kvlite.WithDriver("leveldb"))
```

| Module | Selection name | Native requirement |
| --- | --- | --- |
| `github.com/webong/kvlite/drivers/leveldb` | `leveldb` | None; pure Go |
| `github.com/webong/kvlite/drivers/rocksdb` | `rocksdb` | Build with `-tags rocksdb` and link RocksDB |

The database directory records the selected driver implementation and its
format. Reopening a RocksDB directory through `leveldb` fails with
`ErrBackendMismatch`; migrations must copy logical KVLite records into a new
directory.

Each directory is an independently versioned Go module. Publish compatible
tags together for a release, for example `v0.1.0`,
`drivers/leveldb/v0.1.0`, and `drivers/rocksdb/v0.1.0`. The checked-in
`replace` directives are only for developing this monorepo; consumers resolve
the tagged core and driver modules normally.

A Berkeley DB driver belongs in `drivers/berkeleydb` when its maintainer has
chosen and documented an appropriate distribution. Its source, native library,
and licensing remain outside KVLite core and every unrelated driver module.
