"""ctypes binding for KVLite's stable, versioned C ABI."""

from __future__ import annotations

import ctypes
import os
import platform
from pathlib import Path
from typing import Any, Union

from .codec import decode, encode
from .errors import (
    InvalidArgumentError,
    NativeLibraryError,
    NotFoundError,
    StorageError,
)

Key = Union[str, bytes]

_ABI_VERSION = 1
_STATUS_OK = 0
_STATUS_NOT_FOUND = 1
_STATUS_INVALID_ARGUMENT = 2


class LibraryFinder:
    """Find a release library without depending on a system-wide install."""

    @staticmethod
    def find(requested_path: str | os.PathLike[str] | None = None) -> Path:
        candidates: list[Path] = []
        for candidate in (requested_path, os.environ.get("KVLITE_LIBRARY_PATH")):
            if candidate:
                candidates.append(Path(candidate).expanduser())

        library_name = LibraryFinder._library_name()
        if home := os.environ.get("KVLITE_HOME"):
            candidates.append(Path(home).expanduser() / "lib" / library_name)

        package_root = Path(__file__).resolve().parents[2]
        target = LibraryFinder._target()
        candidates.append(package_root / "native" / target / library_name)

        # Convenient when using the package directly in a KVLite source checkout.
        repository_root = package_root.parents[2]
        candidates.append(repository_root / "dist" / "dev" / target / "lib" / library_name)

        for candidate in candidates:
            if candidate.is_file() and os.access(candidate, os.R_OK):
                return candidate.resolve()

        tried = "\n".join(f"  - {path}" for path in dict.fromkeys(candidates))
        raise NativeLibraryError(
            "KVLite native library was not found. Set KVLITE_LIBRARY_PATH to libkvlite "
            f"or install a matching KVLite native bundle. Looked in:\n{tried}"
        )

    @staticmethod
    def _library_name() -> str:
        system = platform.system()
        if system == "Windows":
            return "kvlite.dll"
        if system == "Darwin":
            return "libkvlite.dylib"
        return "libkvlite.so"

    @staticmethod
    def _target() -> str:
        system = {"Darwin": "darwin", "Windows": "windows"}.get(platform.system(), "linux")
        machine = platform.machine().lower()
        architecture = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine, machine)
        return f"{system}-{architecture}"


class _NativeLibrary:
    def __init__(self, path: Path) -> None:
        try:
            if platform.system() == "Windows" and hasattr(os, "add_dll_directory"):
                os.add_dll_directory(str(path.parent))
            self._library = ctypes.CDLL(str(path))
        except OSError as error:
            raise NativeLibraryError(f"Unable to load KVLite native library at {path}: {error}") from error
        self._configure()
        version = int(self._abi_version())
        if version != _ABI_VERSION:
            raise NativeLibraryError(
                f"KVLite native ABI mismatch: this Python package needs ABI {_ABI_VERSION}, got {version}."
            )

    def _configure(self) -> None:
        self._abi_version = self._library.kvlite_abi_version
        self._abi_version.argtypes = []
        self._abi_version.restype = ctypes.c_uint

        self._open = self._library.kvlite_open
        self._open.argtypes = [ctypes.c_char_p, ctypes.POINTER(ctypes.c_uint64), ctypes.POINTER(ctypes.c_void_p)]
        self._open.restype = ctypes.c_int

        self._close = self._library.kvlite_close
        self._close.argtypes = [ctypes.c_uint64, ctypes.POINTER(ctypes.c_void_p)]
        self._close.restype = ctypes.c_int

        self._put = self._library.kvlite_put
        self._put.argtypes = [
            ctypes.c_uint64,
            ctypes.c_void_p,
            ctypes.c_size_t,
            ctypes.c_void_p,
            ctypes.c_size_t,
            ctypes.c_longlong,
            ctypes.POINTER(ctypes.c_void_p),
        ]
        self._put.restype = ctypes.c_int

        self._get = self._library.kvlite_get
        self._get.argtypes = [
            ctypes.c_uint64,
            ctypes.c_void_p,
            ctypes.c_size_t,
            ctypes.POINTER(ctypes.c_void_p),
            ctypes.POINTER(ctypes.c_size_t),
            ctypes.POINTER(ctypes.c_void_p),
        ]
        self._get.restype = ctypes.c_int

        self._delete = self._library.kvlite_delete
        self._delete.argtypes = [
            ctypes.c_uint64,
            ctypes.c_void_p,
            ctypes.c_size_t,
            ctypes.POINTER(ctypes.c_void_p),
        ]
        self._delete.restype = ctypes.c_int

        self._free = self._library.kvlite_free
        self._free.argtypes = [ctypes.c_void_p]
        self._free.restype = None

    def raise_for_status(self, status: int, error_pointer: ctypes.c_void_p) -> None:
        if status == _STATUS_OK:
            return
        message = "KVLite native operation failed."
        if error_pointer.value:
            try:
                message = ctypes.string_at(error_pointer.value).decode("utf-8", "replace")
            finally:
                self._free(error_pointer)
        if status == _STATUS_NOT_FOUND:
            raise NotFoundError(message)
        if status == _STATUS_INVALID_ARGUMENT:
            raise InvalidArgumentError(message)
        raise StorageError(message)


class NativeDatabase:
    """A SQLite-style in-process KVLite database backed by ``libkvlite``."""

    def __init__(self, library: _NativeLibrary, handle: int) -> None:
        self._library = library
        self._handle = handle

    @classmethod
    def open(
        cls,
        path: str | os.PathLike[str],
        library_path: str | os.PathLike[str] | None = None,
    ) -> "NativeDatabase":
        database_path = os.fspath(path)
        if not database_path:
            raise InvalidArgumentError("KVLite database path is required.")
        library = _NativeLibrary(LibraryFinder.find(library_path))
        out_handle = ctypes.c_uint64()
        out_error = ctypes.c_void_p()
        status = int(library._open(database_path.encode("utf-8"), ctypes.byref(out_handle), ctypes.byref(out_error)))
        library.raise_for_status(status, out_error)
        return cls(library, int(out_handle.value))

    def put(self, key: Key, value: Any, ttl_seconds: int = 0) -> None:
        self.put_bytes(key, encode(value), ttl_seconds)

    def get(self, key: Key) -> Any:
        return decode(self.get_bytes(key))

    def put_bytes(self, key: Key, value: bytes, ttl_seconds: int = 0) -> None:
        self._assert_open()
        raw_key = self._key_bytes(key)
        if ttl_seconds < 0:
            raise InvalidArgumentError("KVLite TTL must be zero or a positive number of seconds.")
        raw_value = bytes(value)
        key_pointer, key_buffer = self._buffer(raw_key)
        value_pointer, value_buffer = self._buffer(raw_value)
        out_error = ctypes.c_void_p()
        status = int(
            self._library._put(
                self._handle,
                key_pointer,
                len(raw_key),
                value_pointer,
                len(raw_value),
                ttl_seconds,
                ctypes.byref(out_error),
            )
        )
        # Keep the C buffers live until the C call has returned.
        _ = key_buffer, value_buffer
        self._library.raise_for_status(status, out_error)

    def get_bytes(self, key: Key) -> bytes:
        self._assert_open()
        raw_key = self._key_bytes(key)
        key_pointer, key_buffer = self._buffer(raw_key)
        out_value = ctypes.c_void_p()
        out_length = ctypes.c_size_t()
        out_error = ctypes.c_void_p()
        status = int(
            self._library._get(
                self._handle,
                key_pointer,
                len(raw_key),
                ctypes.byref(out_value),
                ctypes.byref(out_length),
                ctypes.byref(out_error),
            )
        )
        _ = key_buffer
        self._library.raise_for_status(status, out_error)
        try:
            return ctypes.string_at(out_value.value, out_length.value) if out_length.value else b""
        finally:
            if out_value.value:
                self._library._free(out_value)

    def delete(self, key: Key) -> None:
        self._assert_open()
        raw_key = self._key_bytes(key)
        key_pointer, key_buffer = self._buffer(raw_key)
        out_error = ctypes.c_void_p()
        status = int(self._library._delete(self._handle, key_pointer, len(raw_key), ctypes.byref(out_error)))
        _ = key_buffer
        self._library.raise_for_status(status, out_error)

    def close(self) -> None:
        if self._handle == 0:
            return
        out_error = ctypes.c_void_p()
        status = int(self._library._close(self._handle, ctypes.byref(out_error)))
        self._library.raise_for_status(status, out_error)
        self._handle = 0

    def __enter__(self) -> "NativeDatabase":
        return self

    def __exit__(self, *_: object) -> None:
        self.close()

    def __del__(self) -> None:
        try:
            self.close()
        except Exception:
            pass

    def _assert_open(self) -> None:
        if self._handle == 0:
            raise StorageError("KVLite database is closed.")

    @staticmethod
    def _key_bytes(key: Key) -> bytes:
        raw_key = key.encode("utf-8") if isinstance(key, str) else bytes(key)
        if not raw_key:
            raise InvalidArgumentError("KVLite key is required.")
        return raw_key

    @staticmethod
    def _buffer(value: bytes) -> tuple[ctypes.c_void_p, object | None]:
        if not value:
            return ctypes.c_void_p(), None
        buffer = (ctypes.c_ubyte * len(value)).from_buffer_copy(value)
        return ctypes.cast(buffer, ctypes.c_void_p), buffer
