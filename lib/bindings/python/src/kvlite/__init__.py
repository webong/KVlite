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
    ) -> NativeDatabase:
        return NativeDatabase.open(path, library_path)

    @staticmethod
    def connect(base_url: str, token: str | None = None, timeout_seconds: float = 30) -> HttpDatabase:
        return HttpDatabase(base_url, token, timeout_seconds)


def open(path: str | os.PathLike[str], library_path: str | os.PathLike[str] | None = None) -> NativeDatabase:
    """Open an embedded KVLite database through the C shared library."""
    return KVLite.open(path, library_path)


def connect(base_url: str, token: str | None = None, timeout_seconds: float = 30) -> HttpDatabase:
    """Connect to a KVLite owner process over its stable JSON/HTTP API."""
    return KVLite.connect(base_url, token, timeout_seconds)


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
