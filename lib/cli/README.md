# KVLite CLI project

This distribution project publishes the `kvlite` binary built from
[`cmd/kvlite`](../../cmd/kvlite). The binary is the single owner of the chosen
server-owned driver/path mapping and can expose both the language-neutral HTTP
API and the optional Redis-compatible endpoint. If `--driver` is omitted, a
single-driver bundle uses its one compiled driver; a RocksDB-containing bundle
retains RocksDB as the compatibility default. `kvlite driver list` displays the drivers compiled into
the binary; repeat `--driver-path name=directory` to expose additional mappings
for HTTP clients.

Build only this artifact on the current native platform:

```bash
make release-cli RELEASE_VERSION=v0.1.0 DRIVER=leveldb
```

The resulting binary is placed at
`dist/v0.1.0/<os>-<arch>/drivers/<driver>/bin/kvlite` (or `kvlite.exe` on
Windows).
