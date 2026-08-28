.PHONY: test test-race test-rocksdb test-rocksdb-docker vet build-cli build-c-shared

test:
	go test ./...

test-race:
	go test -race ./...

test-rocksdb:
	go test -tags rocksdb ./...

test-rocksdb-docker:
	bash ./scripts/test-rocksdb-docker.sh

vet:
	go vet ./...

build-cli:
	mkdir -p dist
	go build -tags rocksdb -o dist/kvlite ./cmd/kvlite

build-c-shared:
	mkdir -p dist
	go build -tags rocksdb -buildmode=c-shared -o dist/libkvlite.so ./capi
