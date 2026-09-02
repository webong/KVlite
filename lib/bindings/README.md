# KVLite language bindings

These are real, thin packages over KVLite's public boundaries—not separate
database implementations. Every embedded binding uses ABI version 1 from
[`../../capi/kvlite.h`](../../capi/kvlite.h), calls `kvlite_abi_version()` on
load, and resolves a matching library in this order:

1. an explicit library path;
2. `KVLITE_LIBRARY_PATH`;
3. `KVLITE_HOME/lib`; then
4. a matching `native/<os>-<arch>` package asset or local `dist/dev` release.

| Directory | Package name | Local API | Remote API | Binding test |
| --- | --- | --- | --- | --- |
| [`php/`](php/) | `webong/kvlite` | PHP FFI | JSON/HTTP | `composer --working-dir=lib/bindings/php test` |
| [`python/`](python/) | `kvlite` | `ctypes` | JSON/HTTP | `bash lib/bindings/python/tests/run.sh` |
| [`node/`](node/) | `@webong/kvlite` | N-API loader | JSON/HTTP | `npm --prefix lib/bindings/node test` |
| [`rust/`](rust/) | `kvlite` | `libloading` | OpenAPI/Redis boundary | `cargo test -p kvlite` |

Use `open()` only when one process owns the RocksDB directory. For PHP-FPM,
Node clusters, worker fleets, or multiple applications, run `kvlite serve` and
use the package's `connect()` API, an OpenAPI-generated client, or a standard
Redis client against KVLite's optional Redis endpoint.

The wrappers serialize normal values as JSON and each native wrapper also has a
raw byte API for applications that choose MessagePack, protobuf, or another
codec. Packages are source-ready for Composer, PyPI, npm, and crates.io; the
native asset-publishing phase remains separate because the current release
artifact does not yet bundle RocksDB's runtime dependencies.
