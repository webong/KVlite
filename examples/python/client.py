"""Minimal Python client for `kvlite serve` (Python 3.9+, stdlib only)."""

import base64
import json
import os
import urllib.request


BASE_URL = os.environ.get("KVLITE_URL", "http://127.0.0.1:8089")
TOKEN = os.environ.get("KVLITE_TOKEN", "")


def key_path(key: str) -> str:
    encoded = base64.urlsafe_b64encode(key.encode()).decode().rstrip("=")
    return f"{BASE_URL}/v1/entries/{encoded}"


def request(method: str, key: str, body=None, ttl_seconds: int = 0):
    url = key_path(key)
    if ttl_seconds:
        url += f"?ttl_seconds={ttl_seconds}"
    data = None if body is None else json.dumps(body).encode()
    headers = {"Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if TOKEN:
        headers["Authorization"] = f"Bearer {TOKEN}"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req) as response:
        if response.status == 204:
            return None
        return json.load(response)


request("PUT", "user:101", {"id": 101, "name": "Ada"}, ttl_seconds=3600)
print(request("GET", "user:101"))
