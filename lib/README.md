# KVLite distribution projects

`lib/` is the source-controlled home for KVLite's distributable projects. It
keeps release contracts, package metadata, and thin language adapters next to
the database engine without mixing generated platform binaries into source
control.

| Path | Purpose | Current build source |
| --- | --- | --- |
| [`cli/`](cli/) | The `kvlite` owner/server binary contract | `cmd/kvlite` |
| [`c-shared/`](c-shared/) | The `libkvlite` embedded FFI contract | `capi` |
| [`bindings/`](bindings/) | PHP, Python, Node.js, and Rust language packages | HTTP, Redis, or C ABI |
| [`../extensions/rocksdb/`](../extensions/rocksdb/) | Optional RocksDB storage-driver extension | `extensions/rocksdb` |
| [`../extensions/leveldb/`](../extensions/leveldb/) | Optional pure-Go LevelDB storage-driver extension | `extensions/leveldb` |
| [`../extensions/berkeleydb/`](../extensions/berkeleydb/) | Optional Berkeley DB CGo storage-driver extension | Not bundled; application owner supplies a licensed library |
| [`../extensions/http/`](../extensions/http/) | Optional Go HTTP server/client module | `extensions/http` |
| [`../extensions/redis/`](../extensions/redis/) | Optional Redis-compatible server module | `extensions/redis` |
| [`manifest.json`](manifest.json) | Public artifact names and supported target inventory | `scripts/build-release.sh` |
| [`../MODULES.md`](../MODULES.md) | Standalone module manifest, discovery, and verification contract | core module catalog |

Run `make release RELEASE_VERSION=v0.1.0 DRIVER=leveldb` (or `DRIVER=rocksdb`)
on a native target to create one storage-driver extension bundle, and
`make release-http` / `make release-redis` for the standalone protocol
executable bundles:

```text
dist/v0.1.0/<os>-<arch>/drivers/<driver>/
├── bin/kvlite[.exe]                  # extension-free host by default
├── include/kvlite.h
├── lib/libkvlite.{so,dylib}  # kvlite.dll on Windows
├── kvlite-module.json
└── SHA256SUMS

dist/v0.1.0/<os>-<arch>/modules/{http,redis}/
├── bin/kvlite-{http,redis}[.exe]    # no statically linked storage driver
├── kvlite-module.json
└── SHA256SUMS
```

The output directory is intentionally ignored by Git. A release is assembled
by CI from the artifacts produced on each native operating-system/architecture
runner, then its checksums are published with the release.

For a self-contained driver bundle that runs without system-wide native
installs, add `--bundle-runtime` with the third-party license notices it
requires:

```bash
make release-runtime RELEASE_VERSION=v0.1.0 DRIVER=rocksdb \
  RELEASE_NOTICES="third-party/NOTICE-rocksdb third-party/NOTICE-snappy"
```

This copies non-system runtime libraries (RocksDB, compression, C++
runtime) into the bundle, rewrites loader paths to it (`@loader_path`
on macOS, `$ORIGIN` on Linux via patchelf, DLLs beside the binaries on
Windows through application-directory-first lookup), re-signs Mach-O
binaries, and checksums everything including `NOTICES/`. Linux needs
`patchelf`; Windows needs `dumpbin` (MSVC) or `objdump` (MinGW) and still
awaits runner proof. Verify the mechanism without native dependencies via
`make test-bundle-runtime`.

The on-disk `drivers/<driver>` bundle path is retained for compatibility with
the first language-binding installers. It is a release-layout detail; all
optional source modules, including drivers, live under `extensions/`.

This first build layer creates one bundle per selected driver. RocksDB support
is compiled against the target's installed runtime; it does not yet bundle or
relocate RocksDB and compression-library dependencies. LevelDB's bundle links
only the pure-Go LevelDB driver. Before publishing a self-contained installer,
the packaging layer must bundle RocksDB's native dependencies, set each
platform's loader paths, and include their license notices.

The C shared-library artifact is intentionally embedded-only. The driver
bundle's `kvlite-module.json` makes its checksummed artifacts discoverable by
the generic module catalog, and the installed HTTP and Redis extensions use the
same metadata contract as executable modules. The default CLI links neither
protocol extension (built with `kvlite_no_linked_extensions`); it launches a
verified standalone executable instead. A linked CLI with both protocols is
only a development convenience (`--linked-extensions`), not the release
profile.
