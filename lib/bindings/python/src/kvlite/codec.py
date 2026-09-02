"""JSON serialization shared by every public KVLite transport."""

from __future__ import annotations

import json
from typing import Any

from .errors import SerializationError


def encode(value: Any) -> bytes:
    try:
        return json.dumps(
            value,
            ensure_ascii=False,
            allow_nan=False,
            separators=(",", ":"),
        ).encode("utf-8")
    except (TypeError, ValueError) as error:
        raise SerializationError(f"KVLite value is not JSON-serializable: {error}") from error


def decode(payload: bytes) -> Any:
    try:
        return json.loads(payload.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise SerializationError(f"KVLite returned invalid JSON: {error}") from error
