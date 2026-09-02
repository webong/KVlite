"""The public exception hierarchy shared by native and HTTP KVLite clients."""


class KVLiteError(RuntimeError):
    """Base class for all KVLite client failures."""


class NotFoundError(KVLiteError):
    """Raised when a live key does not exist."""


class InvalidArgumentError(KVLiteError):
    """Raised before or by a KVLite call with invalid input."""


class StorageError(KVLiteError):
    """Raised for a native or remote storage failure."""


class SerializationError(KVLiteError):
    """Raised when a value cannot be represented as JSON."""


class NativeLibraryError(KVLiteError):
    """Raised when the matching libkvlite shared library cannot be loaded."""
