# RocksDB test container

This service gives KVLite a reproducible native build environment. It uses the
official Go Bookworm image, builds a pinned RocksDB release from source, enables
cgo, and runs the complete suite with the `rocksdb` build tag. The default is
RocksDB `v10.8.3`; set `ROCKSDB_VERSION` to exercise another supported release.

The image keeps compiler warnings visible but does not promote them to errors;
that avoids a GCC 12 false-positive in RocksDB 10.8.3's `unique_id.cc`.

The final image deliberately installs the compression *development* packages,
not just their runtime libraries: cgo's linker needs unversioned `libzstd.so`,
`liblz4.so`, `libz.so`, and `libsnappy.so` linker names.

From the repository root:

```bash
bash ./scripts/test-rocksdb-docker.sh
```

To test a specific RocksDB release:

```bash
ROCKSDB_VERSION=v10.10.1 bash ./scripts/test-rocksdb-docker.sh
```

Run the checked compatibility range with `make test-rocksdb-compat`.

The equivalent Compose command is:

```bash
docker compose -f compose.rocksdb.yml run --rm --build rocksdb-test
```

The source tree is copied into the image at build time. Re-run with `--build`
after changing Go code so the test image contains the current checkout.
