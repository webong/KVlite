# Berkeley DB driver extension

This is KVLite's opt-in adapter for the Berkeley DB C API. It stores one Btree
file named `KVLITE-BERKELEYDB.db` beside KVLite's normal
`KVLITE-MANIFEST.json`, so it cannot be confused with RocksDB or LevelDB data.
It is never imported by KVLite core, enabled by the default CLI, or included in
the default release bundles.

## Build it explicitly

The adapter targets the portable modern Berkeley DB C API, not one exact
Berkeley DB release. It needs matching headers and a native library supplied
by the application owner. Select a distribution whose terms fit the
application before installing it. The Homebrew package is an AGPL option; it
is suitable only when those obligations are appropriate for the application.

For a local, AGPL-compatible Homebrew build on macOS:

```bash
brew install berkeley-db

BDB_PREFIX="$(brew --prefix berkeley-db)"
make test-berkeleydb \
  BERKELEYDB_CFLAGS="-I${BDB_PREFIX}/include" \
  BERKELEYDB_LDFLAGS="-L${BDB_PREFIX}/lib"
```

For an embedded Go application, import the extension and build with the
`berkeleydb` tag:

```go
import _ "github.com/webong/kvlite/extensions/berkeleydb"
```

```bash
CGO_CFLAGS="-I/path/to/berkeleydb/include" \
CGO_LDFLAGS="-L/path/to/berkeleydb/lib" \
go build -tags berkeleydb ./your-app
```

To include it in the `kvlite` CLI or `libkvlite` C ABI bundle, use both build
tags: `berkeleydb,kvlite_berkeleydb`:

```bash
make build-cli DRIVER=berkeleydb \
  BERKELEYDB_CFLAGS="-I${BDB_PREFIX}/include" \
  BERKELEYDB_LDFLAGS="-L${BDB_PREFIX}/lib"
```

## License boundary

This source adapter is not a Berkeley DB distribution. Oracle documents its
18.1 OTN packages as AGPLv3 and its commercial packages as subject to Oracle's
commercial agreement. Choose a distribution and comply with its terms before
building or redistributing a Berkeley DB-enabled binary. KVLite does not
promise that Berkeley DB's own files can be exchanged across arbitrary engine
versions; test the version you choose. See Oracle's
[license-change notice](https://docs.oracle.com/database/bdb181/html/installation/license_change.html)
and [licensing overview](https://www.oracle.com/database/technologies/related/berkeleydb/berkeleydb-licensing.html).

KVLite deliberately does not publish a Berkeley DB artifact from the standard
release workflow. A separately distributed binary module must record its exact
Berkeley DB source, version, license basis, checksums, notices, and supported
platform matrix.
