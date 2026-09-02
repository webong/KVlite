"""Integration check that loads a real c-shared KVLite build, not a mock."""

from __future__ import annotations

import tempfile

import kvlite


with tempfile.TemporaryDirectory(prefix="kvlite-python-real-") as directory:
    with kvlite.open(directory) as database:
        database.put("user:101", {"id": 101, "name": "Ada"}, ttl_seconds=60)
        assert database.get("user:101") == {"id": 101, "name": "Ada"}

print("real libkvlite Python binding passed")
