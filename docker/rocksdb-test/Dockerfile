FROM golang:1.23-bookworm AS rocksdb-builder

ARG ROCKSDB_VERSION=v10.8.3

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        build-essential \
        ca-certificates \
        git \
        libbz2-dev \
        liblz4-dev \
        libsnappy-dev \
        libzstd-dev \
        zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /source

# Build a pinned RocksDB release so every container test exercises a known
# native ABI instead of whatever version the base distribution happens to ship.
RUN git clone --depth 1 --branch "${ROCKSDB_VERSION}" https://github.com/facebook/rocksdb.git rocksdb

WORKDIR /source/rocksdb
RUN make -j"$(nproc)" DISABLE_WARNING_AS_ERROR=1 shared_lib \
    && install -d /opt/rocksdb/include /opt/rocksdb/lib \
    && cp -a include/rocksdb /opt/rocksdb/include/ \
    && cp -a librocksdb.so* /opt/rocksdb/lib/

FROM golang:1.23-bookworm

ENV CGO_ENABLED=1 \
    GOTOOLCHAIN=local \
    GOCACHE=/go/build-cache \
    CGO_CFLAGS=-I/opt/rocksdb/include \
    CGO_LDFLAGS=-L/opt/rocksdb/lib \
    LD_LIBRARY_PATH=/opt/rocksdb/lib

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        libbz2-dev \
        liblz4-dev \
        python3 \
        libsnappy-dev \
        libstdc++6 \
        libzstd-dev \
        zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

COPY --from=rocksdb-builder /opt/rocksdb /opt/rocksdb

WORKDIR /workspace

# Keep dependency downloads in an early layer so source-only changes rebuild
# quickly while still compiling against the pinned RocksDB headers and library.
COPY go.work go.mod go.sum ./
COPY capi/go.mod capi/go.sum ./capi/
COPY cmd/kvlite/go.mod cmd/kvlite/go.sum ./cmd/kvlite/
COPY extensions/leveldb/go.mod extensions/leveldb/go.sum ./extensions/leveldb/
COPY extensions/rocksdb/go.mod extensions/rocksdb/go.sum ./extensions/rocksdb/
COPY extensions/http/go.mod ./extensions/http/
COPY extensions/redis/go.mod ./extensions/redis/
COPY examples/basic/go.mod examples/basic/go.sum ./examples/basic/
RUN go mod download \
    && (cd capi && go mod download) \
    && (cd cmd/kvlite && go mod download) \
    && (cd extensions/leveldb && go mod download) \
    && (cd extensions/rocksdb && go mod download) \
    && (cd extensions/http && go mod download) \
    && (cd extensions/redis && go mod download) \
    && (cd examples/basic && go mod download)

COPY . .

CMD ["sh", "-c", "go test -tags 'rocksdb,kvlite_rocksdb' . ./capi ./cmd/kvlite ./extensions/rocksdb/... ./extensions/http/... ./extensions/redis/... ./examples/basic"]
