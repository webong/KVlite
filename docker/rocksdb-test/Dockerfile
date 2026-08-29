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
        libsnappy-dev \
        libstdc++6 \
        libzstd-dev \
        zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

COPY --from=rocksdb-builder /opt/rocksdb /opt/rocksdb

WORKDIR /workspace

# Keep dependency downloads in an early layer so source-only changes rebuild
# quickly while still compiling against the pinned RocksDB headers and library.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

CMD ["go", "test", "-tags", "rocksdb", "./..."]
