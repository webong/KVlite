# KVLite language bindings

Thin language packages belong here as they are introduced:

- `python/` for `kvlite-python`;
- `node/` for `@webong/kvlite`;
- `php/` for the PHP FFI package; and
- `rust/` for the Rust crate.

Each package should select one transport rather than reimplement storage:

1. HTTP/OpenAPI for a remote owner process;
2. Redis for existing Redis clients; or
3. the C shared library for SQLite-like embedded use.

The first binding should use the release layout in [`../`](../) to locate a
matching prebuilt shared library and fall back to a documented remote mode when
native loading is unavailable.
