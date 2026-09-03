"""Dependency-free client for KVLite's public JSON/HTTP protocol."""

from __future__ import annotations

import base64
from collections.abc import Callable
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlparse
from urllib.request import Request, urlopen

from .codec import decode, encode
from .errors import InvalidArgumentError, NotFoundError, StorageError
from .native import Key

Requester = Callable[[str, str, bytes | None, dict[str, str]], tuple[int, bytes]]


class HttpDatabase:
    """A remote KVLite JSON client with no native dependency."""

    def __init__(
        self,
        base_url: str,
        token: str | None = None,
        timeout_seconds: float = 30,
        requester: Requester | None = None,
        driver: str | None = None,
    ) -> None:
        parsed = urlparse(base_url)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise InvalidArgumentError("KVLite remote URL must be a valid http(s) URL.")
        if timeout_seconds <= 0:
            raise InvalidArgumentError("KVLite HTTP timeout must be positive.")
        if driver is not None:
            if not isinstance(driver, str) or not (driver := driver.strip().lower()):
                raise InvalidArgumentError("KVLite remote driver must be a non-empty string when provided.")
        self._base_url = base_url.rstrip("/")
        self._token = token
        self._timeout_seconds = timeout_seconds
        self._driver = driver
        self._requester = requester

    def put(self, key: Key, value: Any, ttl_seconds: int = 0) -> None:
        raw_key = self._key_bytes(key)
        if ttl_seconds < 0:
            raise InvalidArgumentError("KVLite TTL must be zero or a positive number of seconds.")
        path = f"/v1/entries/{self._encoded_key(raw_key)}"
        if ttl_seconds:
            path += f"?ttl_seconds={ttl_seconds}"
        status, body = self._request("PUT", path, encode(value))
        if status != 204:
            raise self._response_error(status, body)

    def get(self, key: Key) -> Any:
        raw_key = self._key_bytes(key)
        status, body = self._request("GET", f"/v1/entries/{self._encoded_key(raw_key)}")
        if status == 404:
            raise NotFoundError("KVLite key was not found.")
        if status != 200:
            raise self._response_error(status, body)
        return decode(body)

    def delete(self, key: Key) -> None:
        raw_key = self._key_bytes(key)
        status, body = self._request("DELETE", f"/v1/entries/{self._encoded_key(raw_key)}")
        if status != 204:
            raise self._response_error(status, body)

    def close(self) -> None:
        """Provided for parity with NativeDatabase; urllib has no resource to close."""

    def _request(self, method: str, path: str, body: bytes | None = None) -> tuple[int, bytes]:
        headers = {"Accept": "application/json"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        if self._driver:
            headers["X-KVLite-Driver"] = self._driver
        url = self._base_url + path
        if self._requester is not None:
            return self._requester(method, url, body, headers)
        request = Request(url, data=body, method=method, headers=headers)
        try:
            with urlopen(request, timeout=self._timeout_seconds) as response:  # noqa: S310: caller chooses base URL.
                return response.status, response.read()
        except HTTPError as error:
            return error.code, error.read()
        except URLError as error:
            raise StorageError(f"KVLite HTTP request failed: {error.reason}") from error

    @staticmethod
    def _key_bytes(key: Key) -> bytes:
        raw_key = key.encode("utf-8") if isinstance(key, str) else bytes(key)
        if not raw_key:
            raise InvalidArgumentError("KVLite key is required.")
        return raw_key

    @staticmethod
    def _encoded_key(key: bytes) -> str:
        return base64.urlsafe_b64encode(key).rstrip(b"=").decode("ascii")

    @staticmethod
    def _response_error(status: int, body: bytes) -> StorageError:
        message = body.decode("utf-8", "replace").strip() or f"KVLite HTTP request failed with status {status}."
        return StorageError(message)
