# KVLite modules

KVLite is embedded by default. A module is an independently distributed,
versioned capability: a storage driver, HTTP, Redis, a codec, or a future
backup/migration provider. Discovering a module never starts it, loads a shared
library, or changes a database.

The module catalog takes the useful parts of both common extension models:
SQLite-style explicit native artifacts, and PostgreSQL-style package metadata,
versioning, dependencies, and verification.

## The manifest contract

Every packaged module has a `kvlite-module.json` file. Schema and module ABI
version `1` require at least:

```json
{
  "schema_version": 1,
  "name": "rocksdb",
  "kind": "driver",
  "version": "v0.1.0",
  "module_abi": 1,
  "driver": "rocksdb",
  "capabilities": ["embedded-storage", "ttl-compaction"],
  "license": "Apache-2.0",
  "artifacts": [
    {
      "platform": "darwin-arm64",
      "kind": "c-shared",
      "path": "lib/libkvlite.dylib",
      "sha256": "...",
      "symbol": "kvlite_abi_version"
    }
  ]
}
```

Artifact paths are relative to the manifest, checksums are SHA-256, and a
module name can only appear once in a discovery set. Unknown manifest fields,
path traversal, incompatible module ABI versions, and duplicate module names
are rejected.

The checksum detects damaged or unexpectedly replaced artifacts after a
trusted installation; it is not itself a publisher signature. Release tooling
must still authenticate the module manifest or enclosing release before it is
installed.

KVLite searches only explicit locations:

1. `KVLITE_MODULE_PATH`, a platform path-list;
2. `KVLITE_HOME/modules`;
3. `KVLITE_HOME/drivers`, retained for the existing driver-bundle layout.

It never implicitly searches the current directory. Inspect an installation
without executing it:

```bash
kvlite module list
kvlite module verify rocksdb
```

## Driver selection

The caller selects a driver when a local database is first opened. KVLite then
stores that stable driver identity in `KVLITE-MANIFEST.json`. Reopening through
a different driver fails; the manifest does not make RocksDB, LevelDB, and
Berkeley DB files interchangeable.

For the current remote HTTP server, an authenticated client may request a
driver name and the server resolves only an installed, server-owned mapping.
A missing driver returns `driver_not_installed`; an installed but unexposed one
returns `driver_not_exposed`. Remote clients never choose a filesystem path.

For in-process calls, local `WithDriver` resolution now also checks installed
driver modules. If a discoverable module matches the requested name, KVLite
returns a dedicated error that tells you the module is present but not linked into
the current process.

When KVLite adds named remote database creation, that same request will select
the initial driver and persist it in the database manifest. Later requests for
a different driver will fail rather than guessing or converting storage files.

## HTTP and Redis

HTTP and Redis use the same module metadata and discovery rules as drivers,
but a transport listener remains server policy. A client chooses HTTP or Redis
by connecting to that endpoint; it cannot bind a server socket merely by
asking for an extension.

The current Go packages are linked-module implementations:

```go
import kvlitehttp "github.com/webong/kvlite/extensions/http"
import kvliteredis "github.com/webong/kvlite/extensions/redis"
```

They register their manifests when imported and start only when `Serve` is
called. This preserves the embedded-first default while the release layer
gains standalone protocol artifacts.

The standalone transport form is an executable module started explicitly by the
KVLite owner. It communicates with that one owner over authenticated private
local IPC; it must not open the same database directory in a second process.
This permits installed HTTP and Redis modules without relying on Go's
toolchain-coupled `plugin` mechanism.

## Current release transition

`scripts/build-release.sh` now emits a checksummed `kvlite-module.json` with
each selected driver bundle. Today a driver bundle is a prebuilt `libkvlite`
C shared library and/or `kvlite` executable containing core plus that driver;
language bindings can use it without compiling Go or RocksDB.

All optional source modules now live under `extensions/*`: RocksDB, LevelDB,
Berkeley DB, HTTP, and Redis. Linked Go modules
register the same metadata as their standalone bundles. This keeps the normal
Go development workflow working while giving all consumers one catalog format.
A future native-module loader will use a stable C initialization function (for
example `kvlite_module_init_v1`), not Go runtime plugins.

`extensions/berkeleydb` is a CGo adapter, not a Berkeley DB binary
distribution. It does not change licensing for any other KVLite module and is
absent from standard release bundles. The application owner must explicitly
provide a Berkeley DB distribution under terms it is entitled to use; otherwise
the driver reports `ErrBerkeleyDBNotBuilt`.
