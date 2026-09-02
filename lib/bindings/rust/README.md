# `kvlite` for Rust

The Rust crate is a small, safe wrapper over the stable KVLite C ABI. It
dynamically loads `libkvlite`, so it behaves like a local embedded database
library without linking your application directly to RocksDB.

Use it when one process owns the database directory. For workers or several
applications, run `kvlite serve` and use KVLite's JSON/HTTP OpenAPI contract or
its optional Redis endpoint instead.

## Install

Until the crate is published on crates.io, use the public Git repository with
the Rust package directory:

```toml
[dependencies]
kvlite = { git = "https://github.com/webong/KVlite", package = "kvlite" }
```

## Embedded use

Build or download the matching native library and set its exact path:

```bash
export KVLITE_LIBRARY_PATH=/opt/kvlite/lib/libkvlite.dylib # .so on Linux
```

```rust
use std::time::Duration;
use kvlite::Database;
use serde::{Deserialize, Serialize};

#[derive(Debug, Serialize, Deserialize)]
struct User { id: u64, name: String }

let mut db = Database::open("./data")?;
db.put("user:101", &User { id: 101, name: "Ada".into() }, Some(Duration::from_secs(3600)))?;
let user: User = db.get("user:101")?;
db.close()?;
# Ok::<(), kvlite::Error>(())
```

Use `put_bytes()` and `get_bytes()` when the application owns a MessagePack,
protobuf, or other binary codec.

## Test

```bash
cargo test
```

The test compiles a small ABI-compatible C mock and exercises real dynamic
loading; RocksDB itself is not required for the binding test.
