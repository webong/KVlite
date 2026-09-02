# `kvlite` for Python

The Python package provides a local embedded API and a dependency-free remote
API:

- `kvlite.open()` loads KVLite's stable C library with `ctypes`, like SQLite
  loads its native engine in-process.
- `kvlite.connect()` talks to a KVLite owner through the public JSON/HTTP API.

Open a directory locally only from one owning process. For workers, multiple
applications, or an existing Redis codebase, run `kvlite serve` and use the
HTTP or Redis endpoint instead.

## Install

Until the package is published on PyPI, install it from the public repository:

```bash
python -m pip install 'git+https://github.com/webong/KVlite.git#subdirectory=lib/bindings/python'
```

## Embedded use

Build or download the native library for the same OS/architecture, then set
its explicit path:

```bash
export KVLITE_LIBRARY_PATH=/opt/kvlite/lib/libkvlite.dylib  # .so on Linux
```

```python
import kvlite

with kvlite.open("./data") as db:
    db.put("user:101", {"id": 101, "name": "Ada"}, ttl_seconds=3600)
    user = db.get("user:101")
```

`NativeDatabase` also exposes `put_bytes()` / `get_bytes()` for MessagePack,
protobuf, or another application-owned binary codec.

## Remote use

```python
import kvlite

db = kvlite.connect("http://127.0.0.1:8089", token="your-token")
db.put("user:101", {"id": 101, "name": "Ada"})
```

This path has no native dependency and uses KVLite's versioned JSON protocol.

## Test

```bash
bash tests/run.sh
```

The suite compiles a tiny C ABI mock and runs the actual `ctypes` bridge, so it
does not need a local RocksDB installation.
