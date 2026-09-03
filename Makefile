.PHONY: test test-race test-rocksdb test-rocksdb-docker test-rocksdb-compat test-bindings-docker test-bindings-leveldb vet build-cli build-c-shared release release-cli release-c-shared

RELEASE_VERSION ?= dev
RELEASE_TARGET ?= $(shell go env GOHOSTOS)-$(shell go env GOHOSTARCH)
ROCKSDB_VERSION ?= v10.8.3
DRIVER ?= rocksdb
DRIVER_TAGS ?= $(if $(filter rocksdb,$(DRIVER)),rocksdb kvlite_rocksdb,kvlite_leveldb)

test:
	go test . ./capi ./cmd/kvlite ./drivers/leveldb/... ./drivers/rocksdb/... ./extensions/http/... ./extensions/redis/...

test-race:
	go test -race . ./capi ./cmd/kvlite ./drivers/leveldb/... ./drivers/rocksdb/... ./extensions/http/... ./extensions/redis/...

test-rocksdb:
	go test -tags 'rocksdb,kvlite_rocksdb' . ./capi ./cmd/kvlite ./drivers/rocksdb/... ./extensions/http/... ./extensions/redis/... ./examples/basic

test-rocksdb-docker:
	ROCKSDB_VERSION="$(ROCKSDB_VERSION)" bash ./scripts/test-rocksdb-docker.sh

# Exercise KVLite's portable native adapter at both ends of the supported
# RocksDB 10.x range. The source builds are cached separately by Docker.
test-rocksdb-compat:
	ROCKSDB_VERSION=v10.8.3 bash ./scripts/test-rocksdb-docker.sh
	ROCKSDB_VERSION=v10.10.1 bash ./scripts/test-rocksdb-docker.sh

# Build a real c-shared library in the RocksDB container and load it through
# the Python ctypes package. This complements the dependency-free ABI mocks.
test-bindings-docker:
	bash ./scripts/test-bindings-docker.sh

# Build a pure-Go LevelDB c-shared library and exercise the real Python ctypes
# driver-selection path without requiring RocksDB headers or Docker.
test-bindings-leveldb:
	bash ./scripts/test-bindings-leveldb.sh

vet:
	go vet . ./capi ./cmd/kvlite ./drivers/leveldb/... ./drivers/rocksdb/... ./extensions/http/... ./extensions/redis/...

build-cli:
	mkdir -p dist
	go build -tags '$(DRIVER_TAGS)' -o dist/kvlite ./cmd/kvlite

build-c-shared:
	mkdir -p dist
	go build -tags '$(DRIVER_TAGS)' -buildmode=c-shared -o dist/libkvlite.so ./capi

# Build distributable artifacts for the current native platform. RocksDB uses
# cgo, so release builds intentionally run on the matching OS and CPU.
release:
	bash ./scripts/build-release.sh --version "$(RELEASE_VERSION)" --target "$(RELEASE_TARGET)" --driver "$(DRIVER)"

release-cli:
	bash ./scripts/build-release.sh --version "$(RELEASE_VERSION)" --target "$(RELEASE_TARGET)" --driver "$(DRIVER)" --component cli

release-c-shared:
	bash ./scripts/build-release.sh --version "$(RELEASE_VERSION)" --target "$(RELEASE_TARGET)" --driver "$(DRIVER)" --component c-shared
