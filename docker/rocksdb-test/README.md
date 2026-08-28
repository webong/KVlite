# RocksDB test container

This service gives KVLite a reproducible native build environment. It uses the
official Go Bookworm image, installs Debian's `librocksdb-dev`, enables cgo,
and runs the complete suite with the `rocksdb` build tag.

From the repository root:

```bash
./scripts/test-rocksdb-docker.sh
```

The equivalent Compose command is:

```bash
docker compose -f compose.rocksdb.yml run --rm --build rocksdb-test
```

The source tree is copied into the image at build time. Re-run with `--build`
after changing Go code so the test image contains the current checkout.
