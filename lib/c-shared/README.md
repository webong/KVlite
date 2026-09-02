# KVLite C shared-library project

This distribution project publishes the embedded `libkvlite` ABI built from
[`capi`](../../capi). The checked-in
[`capi/kvlite.h`](../../capi/kvlite.h) is the public header that ships in every
release. It is deliberately separate from Go's generated cgo header so status
codes and allocator ownership remain documented and stable.

Build only this artifact on the current native platform:

```bash
make release-c-shared RELEASE_VERSION=v0.1.0
```

The output contains the platform library and `include/kvlite.h`. ABI version 1
is checked by the Python, Node.js, PHP, and Rust adapters in
[`../bindings/`](../bindings/) before any database handle is opened; none of
them embeds a second database implementation.
