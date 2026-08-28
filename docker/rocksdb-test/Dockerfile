FROM golang:1.23-bookworm

ENV CGO_ENABLED=1 \
    GOTOOLCHAIN=local \
    GOCACHE=/go/build-cache

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        librocksdb-dev \
        pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

# Keep dependency downloads in an early layer so source-only changes rebuild
# quickly while still compiling against the system RocksDB headers and library.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

CMD ["go", "test", "-tags", "rocksdb", "./..."]
