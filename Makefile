.PHONY: test test-race test-rocksdb test-rocksdb-docker test-rocksdb-compat test-berkeleydb test-bindings-docker test-bindings-leveldb vet build-cli build-c-shared release release-cli release-c-shared release-berkeleydb

RELEASE_VERSION ?= dev
RELEASE_TARGET ?= $(shell go env GOHOSTOS)-$(shell go env GOHOSTARCH)
ROCKSDB_VERSION ?= v10.8.3
# These default to any cgo flags already supplied by the application owner.
# The Berkeley DB target below only consumes them for DRIVER=berkeleydb.
BERKELEYDB_CFLAGS ?= $(CGO_CFLAGS)
BERKELEYDB_LDFLAGS ?= $(CGO_LDFLAGS)
DRIVER ?= rocksdb
DRIVER_TAGS ?= $(if $(filter rocksdb,$(DRIVER)),rocksdb kvlite_rocksdb,$(if $(filter leveldb,$(DRIVER)),kvlite_leveldb,$(if $(filter berkeleydb,$(DRIVER)),berkeleydb kvlite_berkeleydb,)))

ifeq ($(DRIVER),berkeleydb)
DRIVER_CGO_ENV = CGO_CFLAGS="$(BERKELEYDB_CFLAGS)" CGO_LDFLAGS="$(BERKELEYDB_LDFLAGS)"
else
DRIVER_CGO_ENV =
endif

test:
	go test . ./capi ./cmd/kvlite ./extensions/berkeleydb/... ./extensions/leveldb/... ./extensions/rocksdb/... ./extensions/http/... ./extensions/redis/...

test-race:
	go test -race . ./capi ./cmd/kvlite ./extensions/berkeleydb/... ./extensions/leveldb/... ./extensions/rocksdb/... ./extensions/http/... ./extensions/redis/...

test-rocksdb:
	go test -tags 'rocksdb,kvlite_rocksdb' . ./capi ./cmd/kvlite ./extensions/rocksdb/... ./extensions/http/... ./extensions/redis/... ./examples/basic

# Berkeley DB is intentionally not part of the default or release suites.
# Supply headers and a library from a Berkeley DB distribution you are licensed
# to use, for example CGO_CFLAGS=-I... CGO_LDFLAGS=-L.../lib -ldb.
test-berkeleydb:
	CGO_CFLAGS="$(BERKELEYDB_CFLAGS)" CGO_LDFLAGS="$(BERKELEYDB_LDFLAGS)" go test -tags 'berkeleydb,kvlite_berkeleydb' . ./capi ./cmd/kvlite ./extensions/berkeleydb/...

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
	go vet . ./capi ./cmd/kvlite ./extensions/berkeleydb/... ./extensions/leveldb/... ./extensions/rocksdb/... ./extensions/http/... ./extensions/redis/...

build-cli:
	mkdir -p dist
	$(DRIVER_CGO_ENV) go build -tags '$(DRIVER_TAGS)' -o dist/kvlite ./cmd/kvlite

build-c-shared:
	mkdir -p dist
	$(DRIVER_CGO_ENV) go build -tags '$(DRIVER_TAGS)' -buildmode=c-shared -o dist/libkvlite.so ./capi

# Build distributable artifacts for the current native platform. RocksDB uses
# cgo, so release builds intentionally run on the matching OS and CPU.
release:
	bash ./scripts/build-release.sh --version "$(RELEASE_VERSION)" --target "$(RELEASE_TARGET)" --driver "$(DRIVER)"

release-cli:
	bash ./scripts/build-release.sh --version "$(RELEASE_VERSION)" --target "$(RELEASE_TARGET)" --driver "$(DRIVER)" --component cli

release-c-shared:
	bash ./scripts/build-release.sh --version "$(RELEASE_VERSION)" --target "$(RELEASE_TARGET)" --driver "$(DRIVER)" --component c-shared

release-berkeleydb:
	ALLOW_BERKELEYDB_BUNDLE=1 \
	./scripts/build-release.sh --version "$(RELEASE_VERSION)" --target "$(RELEASE_TARGET)" --driver berkeleydb --allow-berkeleydb
