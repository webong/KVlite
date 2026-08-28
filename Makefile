.PHONY: test test-race test-rocksdb vet build-c-shared

test:
	go test ./...

test-race:
	go test -race ./...

test-rocksdb:
	go test -tags rocksdb ./...

vet:
	go vet ./...

build-c-shared:
	mkdir -p dist
	go build -tags rocksdb -buildmode=c-shared -o dist/libkvlite.so ./capi
