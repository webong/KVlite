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
| [`manifest.json`](manifest.json) | Public artifact names and supported target inventory | `scripts/build-release.sh` |

Run `make release RELEASE_VERSION=v0.1.0` on a native target to create:

```text
dist/v0.1.0/<os>-<arch>/
├── bin/kvlite[.exe]
├── include/kvlite.h
├── lib/libkvlite.{so,dylib}  # kvlite.dll on Windows
└── SHA256SUMS
```

The output directory is intentionally ignored by Git. A release is assembled
by CI from the artifacts produced on each native operating-system/architecture
runner, then its checksums are published with the release.

This first build layer compiles against the target's installed RocksDB runtime;
it does not yet bundle or relocate RocksDB and compression-library dependencies.
Before publishing a self-contained installer, the packaging layer must bundle
those native dependencies, set each platform's loader paths, and include their
license notices.
