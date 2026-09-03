"""Exercise an actual LevelDB-only c-shared KVLite library default."""

from __future__ import annotations

import tempfile

import kvlite


with tempfile.TemporaryDirectory(prefix="kvlite-leveldb-native-") as directory:
    with kvlite.open(directory) as database:
        database.put("native:leveldb", {"ok": True}, ttl_seconds=60)
        assert database.get("native:leveldb") == {"ok": True}

print("real LevelDB libkvlite Python binding passed")
