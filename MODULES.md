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
4. `KVLITE_SYSTEM_MODULE_PATH`, a platform path-list of read-only system
   roots installed by a package manager; searched last so a user
   installation always shadows the system one. See `packaging/README.md`.

It never implicitly searches the current directory. Inspect an installation
without executing it:

```bash
kvlite module list
./kvlite module run redis -- --path ./data --listen 127.0.0.1:9090
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
caller. A standalone process is a direct owner when given `--path`: it opens
its own `kvlite` database directory and does not yet use shared in-process
IPC. Two direct owners must never open the same directory at once.

When HTTP and Redis must serve the *same* directory, use the shared-owner
topology instead: one `kvlite-http` owner holds the single writable copy of
the directory, and `kvlite-redis --upstream <owner-url>` attaches to it over
the owner's loopback HTTP protocol. The owner must outlive attached
processes; per-command failures (owner down, token rejected) surface as Redis
`ERR` replies without stopping the server. Attached multi-step commands are
not atomic — each record operation crosses the transport separately — so use a
direct owner when commands must observe one coherent snapshot.

```bash
kvlite-http --path ./data --driver leveldb --listen 127.0.0.1:8089 --token "$KVLITE_TOKEN" &
kvlite-redis --upstream http://127.0.0.1:8089 --upstream-token "$KVLITE_TOKEN" \
  --upstream-driver leveldb --listen 127.0.0.1:6379
```

Or let one CLI orchestrate both processes (explicit ports required):

```bash
kvlite serve --path ./data --driver leveldb --extension-mode standalone \
  --listen 127.0.0.1:8089 --redis-listen 127.0.0.1:6379
```

This is sufficient for optional deployment shapes where HTTP or Redis — or
both, through one owner — is the protocol surface for one CLI invocation.

This permits installed HTTP and Redis modules without relying on Go's
toolchain-coupled `plugin` mechanism.

## Installed release layout

`scripts/build-release.sh` emits a checksummed `kvlite-module.json` with every
bundle. A driver bundle is a prebuilt `libkvlite` C shared library and/or a
`kvlite` host/CLI executable containing core plus that driver; language
bindings can use it without compiling Go or RocksDB. Driver release CLIs are
built with `kvlite_no_linked_extensions` by default, so they stay small
host/runners and launch verified standalone protocol executables in `auto` or
`standalone` mode. Pass `--linked-extensions` (or
`KVLITE_LINKED_EXTENSIONS=1`) only for an explicit development/convenience
profile that links HTTP and Redis into the CLI.

Protocol bundles are installed executable modules built from their own Go
module without statically linking any storage driver:

```text
dist/<version>/<os>-<arch>/modules/http/
  bin/kvlite-http[.exe]
  kvlite-module.json
  SHA256SUMS

dist/<version>/<os>-<arch>/modules/redis/
  bin/kvlite-redis[.exe]
  kvlite-module.json
  SHA256SUMS
```

Build them with:

```bash
make release-http RELEASE_VERSION=v0.1.0
make release-redis RELEASE_VERSION=v0.1.0
```

Set `KVLITE_HOME=<install-root>` with driver bundles beneath
`<install-root>/drivers/*` and protocol bundles beneath
`<install-root>/modules/*`; the current `DefaultModulePaths` implementation
discovers both. A protocol executable finds the requested driver's installed
C-shared module and opens it through the runtime driver loader (CGO-enabled
native builds). Loaded driver libraries stay mapped for the process lifetime:
a Go-built shared library starts its own runtime on load and cannot be torn
down safely, so the host closes database handles but never unloads the
library (the same permanence rule as native modules below). Verify everything
before serving:

```bash
kvlite module list
kvlite module verify leveldb
kvlite module verify http
kvlite module verify redis
kvlite serve --path ./data --driver leveldb --extension-mode standalone --listen 127.0.0.1:8080
```

The C embedding ABI (v1) exposes logical put/get/delete plus raw engine
operations (`kvlite_raw_put/get/delete`) and snapshot prefix scans
(`kvlite_raw_scan_open/next/close`) as additive v1 symbols. A standalone
protocol process opens its driver's installed C-shared module through the
runtime driver loader and speaks the engine keyspace directly, so HTTP
put/get/scan and the full Redis data plane (strings, hashes, sets, lists)
work over installed driver modules. Hosts resolve the raw symbols as
required; a bundle that predates them fails to load with a clear
missing-symbol error instead of silently corrupting the keyspace.

## Native in-process modules

A driver can also load in-process through the frozen native-module ABI
(`capi/kvlite_module.h`, v1). The shared library exports one entry point,
`kvlite_module_init_v1`, receives a host-services table, and registers driver
operation tables over the same engine keyspace as the raw C ABI. The host
never unloads an initialized module and verifies its checksum before loading,
like every other artifact kind. Any language that produces a C shared library
can implement one; `capi/testdata/nativememdb/memdb.c` is a dependency-free C
reference used by the loader tests. A driver module manifest for this kind
looks like:

```json
{
  "schema_version": 1,
  "name": "memdb",
  "kind": "driver",
  "version": "v0.1.0",
  "module_abi": 1,
  "driver": "memdb",
  "capabilities": ["embedded-storage"],
  "license": "Apache-2.0",
  "artifacts": [
    {
      "platform": "darwin-arm64",
      "kind": "native-module",
      "path": "memdb.dylib",
      "sha256": "...",
      "symbol": "kvlite_module_init_v1"
    }
  ]
}
```

`Open` prefers a linked driver, then an installed C-shared bundle, then an
installed native module; a missing driver still reports the usual
actionable error. Go's toolchain-coupled `plugin` package remains
unsupported by design.

## Source layout

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
