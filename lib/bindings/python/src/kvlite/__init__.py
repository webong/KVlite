"""KVLite's Python API: embedded ``open`` or remote ``connect``."""

from __future__ import annotations

import os
from typing import Any

from .errors import (
    InvalidArgumentError,
    KVLiteError,
    NativeLibraryError,
    NotFoundError,
    SerializationError,
    StorageError,
)
from .http import HttpDatabase
from .native import NativeDatabase


class KVLite:
    """Factory facade mirroring the familiar local-database open/connect split."""

    @staticmethod
    def open(
        path: str | os.PathLike[str],
        library_path: str | os.PathLike[str] | None = None,
        backend: str = "rocksdb",
        *,
        driver: str | None = None,
    ) -> NativeDatabase:
        return NativeDatabase.open(path, library_path, backend, driver=driver)

    @staticmethod
    def connect(
        base_url: str,
        token: str | None = None,
        timeout_seconds: float = 30,
        driver: str | None = None,
    ) -> HttpDatabase:
        return HttpDatabase(base_url, token, timeout_seconds, driver=driver)


def open(
    path: str | os.PathLike[str],
    library_path: str | os.PathLike[str] | None = None,
    backend: str = "rocksdb",
    *,
    driver: str | None = None,
) -> NativeDatabase:
    """Open an embedded KVLite database through the C shared library."""
    return KVLite.open(path, library_path, backend, driver=driver)


def connect(
    base_url: str,
    token: str | None = None,
    timeout_seconds: float = 30,
    driver: str | None = None,
) -> HttpDatabase:
    """Connect to a KVLite owner process and optionally select an exposed driver."""
    return KVLite.connect(base_url, token, timeout_seconds, driver)


__all__ = [
    "KVLite",
    "NativeDatabase",
    "HttpDatabase",
    "open",
    "connect",
    "KVLiteError",
    "NotFoundError",
    "InvalidArgumentError",
    "StorageError",
    "SerializationError",
    "NativeLibraryError",
]
