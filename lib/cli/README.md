# KVLite CLI project

This distribution project publishes the `kvlite` binary built from
[`cmd/kvlite`](../../cmd/kvlite). The binary is the single owner of a RocksDB
directory and can expose both the language-neutral HTTP API and the optional
Redis-compatible endpoint.

Build only this artifact on the current native platform:

```bash
make release-cli RELEASE_VERSION=v0.1.0
```

The resulting binary is placed at
`dist/v0.1.0/<os>-<arch>/bin/kvlite` (or `kvlite.exe` on Windows).
