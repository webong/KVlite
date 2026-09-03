# KVLite C shared-library project

This distribution project publishes the embedded `libkvlite` ABI built from
[`capi`](../../capi). The checked-in
[`capi/kvlite.h`](../../capi/kvlite.h) is the public header that ships in every
release. It is deliberately separate from Go's generated cgo header so status
codes and allocator ownership remain documented and stable.

This artifact is embedded-only: it neither links nor starts KVLite's optional
HTTP or Redis servers. Deploy the separately installed CLI or the matching Go
extension when a database needs a remote API.

Build only this artifact on the current native platform:

```bash
make release-c-shared RELEASE_VERSION=v0.1.0 DRIVER=leveldb
```

The output contains the platform library and `include/kvlite.h`. ABI version 1
is checked by the Python, Node.js, PHP, and Rust adapters in
[`../bindings/`](../bindings/) before any database handle is opened; none of
them embeds a second database implementation.

ABI v1 also includes additive `kvlite_open_with_driver(path, driver, ...)` and
the compatible `kvlite_open_with_backend(path, backend, ...)` alias. Existing
`kvlite_open(path, ...)` calls select the bundle's default driver; current
bindings can explicitly select any driver built into that bundle.
