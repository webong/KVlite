// Package main exposes the stable C ABI used by embedded language bindings.
//
// Build with:
//
//	go build -tags 'rocksdb,kvlite_rocksdb' -buildmode=c-shared -o libkvlite.so ./capi
package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/webong/kvlite"
)

var databases = struct {
	sync.Mutex
	next  uint64
	items map[uint64]*kvlite.DB
}{items: make(map[uint64]*kvlite.DB), next: 1}

type scanSnapshot struct {
	keys   [][]byte
	values [][]byte
	next   int
}

var cursors = struct {
	sync.Mutex
	next  uint64
	items map[uint64]*scanSnapshot
}{items: make(map[uint64]*scanSnapshot), next: 1}

const (
	statusOK       = C.int(0)
	statusNotFound = C.int(1)
	statusInvalid  = C.int(2)
	statusStorage  = C.int(3)
)

// ABI version is kept in lockstep with capi/kvlite.h. Bindings use it before
// calling the rest of the API so an accidentally mixed library/header pair
// fails deterministically instead of corrupting data.
const abiVersion = C.uint(1)

func main() {}

//export kvlite_abi_version
func kvlite_abi_version() C.uint {
	return abiVersion
}

func setError(out **C.char, err error) {
	if out == nil || err == nil {
		return
	}
	*out = C.CString(err.Error())
}

func statusFor(err error, out **C.char) C.int {
	if err == nil {
		return statusOK
	}
	setError(out, err)
	if errors.Is(err, kvlite.ErrNotFound) {
		return statusNotFound
	}
	if errors.Is(err, kvlite.ErrInvalidArgument) {
		return statusInvalid
	}
	return statusStorage
}

func lookup(handle C.ulonglong, out **C.char) *kvlite.DB {
	databases.Lock()
	db := databases.items[uint64(handle)]
	databases.Unlock()
	if db == nil {
		setError(out, fmt.Errorf("%w: invalid database handle %d", kvlite.ErrInvalidArgument, uint64(handle)))
	}
	return db
}

func inputBytes(pointer unsafe.Pointer, length C.size_t) ([]byte, error) {
	n := uint64(length)
	maxInt := uint64(^uint(0) >> 1)
	if n > maxInt {
		return nil, fmt.Errorf("%w: input is too large", kvlite.ErrInvalidArgument)
	}
	if n == 0 {
		return nil, nil
	}
	if pointer == nil {
		return nil, fmt.Errorf("%w: non-empty input has a nil pointer", kvlite.ErrInvalidArgument)
	}
	return append([]byte(nil), unsafe.Slice((*byte)(pointer), int(n))...), nil
}

func ttlOption(seconds C.longlong) (kvlite.PutOption, error) {
	if seconds < 0 {
		return nil, fmt.Errorf("%w: ttl_seconds must be non-negative", kvlite.ErrInvalidArgument)
	}
	if seconds == 0 {
		return nil, nil
	}
	maxSeconds := int64(^uint64(0)>>1) / int64(time.Second)
	if int64(seconds) > maxSeconds {
		return nil, fmt.Errorf("%w: ttl_seconds is too large", kvlite.ErrInvalidArgument)
	}
	return kvlite.TTL(time.Duration(seconds) * time.Second), nil
}

//export kvlite_open
func kvlite_open(path *C.char, outHandle *C.ulonglong, outError **C.char) C.int {
	if path == nil || outHandle == nil {
		return statusFor(fmt.Errorf("%w: path and output handle are required", kvlite.ErrInvalidArgument), outError)
	}
	db, err := kvlite.Open(C.GoString(path), kvlite.WithDriver(string(kvlite.DefaultDriver())))
	if err != nil {
		return statusFor(err, outError)
	}
	databases.Lock()
	handle := databases.next
	databases.next++
	databases.items[handle] = db
	databases.Unlock()
	*outHandle = C.ulonglong(handle)
	return statusOK
}

func openWithDriver(path *C.char, driver *C.char, outHandle *C.ulonglong, outError **C.char) C.int {
	if path == nil || driver == nil || outHandle == nil {
		return statusFor(fmt.Errorf("%w: path, driver, and output handle are required", kvlite.ErrInvalidArgument), outError)
	}
	db, err := kvlite.Open(C.GoString(path), kvlite.WithDriver(C.GoString(driver)))
	if err != nil {
		return statusFor(err, outError)
	}
	databases.Lock()
	handle := databases.next
	databases.next++
	databases.items[handle] = db
	databases.Unlock()
	*outHandle = C.ulonglong(handle)
	return statusOK
}

//export kvlite_open_with_backend
func kvlite_open_with_backend(path *C.char, backend *C.char, outHandle *C.ulonglong, outError **C.char) C.int {
	return openWithDriver(path, backend, outHandle, outError)
}

//export kvlite_open_with_driver
func kvlite_open_with_driver(path *C.char, driver *C.char, outHandle *C.ulonglong, outError **C.char) C.int {
	return openWithDriver(path, driver, outHandle, outError)
}

//export kvlite_close
func kvlite_close(handle C.ulonglong, outError **C.char) C.int {
	databases.Lock()
	db := databases.items[uint64(handle)]
	if db != nil {
		delete(databases.items, uint64(handle))
	}
	databases.Unlock()
	if db == nil {
		return statusFor(fmt.Errorf("%w: invalid database handle %d", kvlite.ErrInvalidArgument, uint64(handle)), outError)
	}
	return statusFor(db.Close(), outError)
}

//export kvlite_put
func kvlite_put(handle C.ulonglong, key unsafe.Pointer, keyLength C.size_t, value unsafe.Pointer, valueLength C.size_t, ttlSeconds C.longlong, outError **C.char) C.int {
	db := lookup(handle, outError)
	if db == nil {
		return statusInvalid
	}
	keyBytes, err := inputBytes(key, keyLength)
	if err != nil {
		return statusFor(err, outError)
	}
	if len(keyBytes) == 0 {
		return statusFor(fmt.Errorf("%w: key is required", kvlite.ErrInvalidArgument), outError)
	}
	valueBytes, err := inputBytes(value, valueLength)
	if err != nil {
		return statusFor(err, outError)
	}
	ttl, err := ttlOption(ttlSeconds)
	if err != nil {
		return statusFor(err, outError)
	}
	var options []kvlite.PutOption
	if ttl != nil {
		options = []kvlite.PutOption{ttl}
	}
	return statusFor(db.PutBytes(context.Background(), string(keyBytes), valueBytes, options...), outError)
}

//export kvlite_get
func kvlite_get(handle C.ulonglong, key unsafe.Pointer, keyLength C.size_t, outValue *unsafe.Pointer, outLength *C.size_t, outError **C.char) C.int {
	// outValue and outLength are C void** and size_t*.
	db := lookup(handle, outError)
	if db == nil {
		return statusInvalid
	}
	if outValue == nil || outLength == nil {
		return statusFor(fmt.Errorf("%w: output value and length are required", kvlite.ErrInvalidArgument), outError)
	}
	keyBytes, err := inputBytes(key, keyLength)
	if err != nil {
		return statusFor(err, outError)
	}
	if len(keyBytes) == 0 {
		return statusFor(fmt.Errorf("%w: key is required", kvlite.ErrInvalidArgument), outError)
	}
	value, err := db.GetBytes(context.Background(), string(keyBytes))
	if err != nil {
		return statusFor(err, outError)
	}
	// The caller owns this allocation and releases it with kvlite_free.
	*outValue = C.CBytes(value)
	*outLength = C.size_t(len(value))
	return statusOK
}

//export kvlite_delete
func kvlite_delete(handle C.ulonglong, key unsafe.Pointer, keyLength C.size_t, outError **C.char) C.int {
	db := lookup(handle, outError)
	if db == nil {
		return statusInvalid
	}
	keyBytes, err := inputBytes(key, keyLength)
	if err != nil {
		return statusFor(err, outError)
	}
	if len(keyBytes) == 0 {
		return statusFor(fmt.Errorf("%w: key is required", kvlite.ErrInvalidArgument), outError)
	}
	return statusFor(db.Delete(context.Background(), string(keyBytes)), outError)
}

//export kvlite_free
func kvlite_free(pointer unsafe.Pointer) {
	C.free(pointer)
}

func rawStore(handle C.ulonglong, out **C.char) (kvlite.TransportStore, C.int) {
	db := lookup(handle, out)
	if db == nil {
		return nil, statusInvalid
	}
	return db.Transport(), statusOK
}

//export kvlite_raw_put
func kvlite_raw_put(handle C.ulonglong, key unsafe.Pointer, keyLength C.size_t, value unsafe.Pointer, valueLength C.size_t, outError **C.char) C.int {
	store, status := rawStore(handle, outError)
	if store == nil {
		return status
	}
	keyBytes, err := inputBytes(key, keyLength)
	if err != nil {
		return statusFor(err, outError)
	}
	if len(keyBytes) == 0 {
		return statusFor(fmt.Errorf("%w: key is required", kvlite.ErrInvalidArgument), outError)
	}
	valueBytes, err := inputBytes(value, valueLength)
	if err != nil {
		return statusFor(err, outError)
	}
	return statusFor(store.Put(context.Background(), keyBytes, valueBytes), outError)
}

//export kvlite_raw_get
func kvlite_raw_get(handle C.ulonglong, key unsafe.Pointer, keyLength C.size_t, outValue *unsafe.Pointer, outLength *C.size_t, outError **C.char) C.int {
	store, status := rawStore(handle, outError)
	if store == nil {
		return status
	}
	if outValue == nil || outLength == nil {
		return statusFor(fmt.Errorf("%w: output value and length are required", kvlite.ErrInvalidArgument), outError)
	}
	keyBytes, err := inputBytes(key, keyLength)
	if err != nil {
		return statusFor(err, outError)
	}
	if len(keyBytes) == 0 {
		return statusFor(fmt.Errorf("%w: key is required", kvlite.ErrInvalidArgument), outError)
	}
	value, found, err := store.Get(context.Background(), keyBytes)
	if err != nil {
		return statusFor(err, outError)
	}
	if !found {
		return statusFor(kvlite.ErrNotFound, outError)
	}
	// The caller owns this allocation and releases it with kvlite_free.
	*outValue = C.CBytes(value)
	*outLength = C.size_t(len(value))
	return statusOK
}

//export kvlite_raw_delete
func kvlite_raw_delete(handle C.ulonglong, key unsafe.Pointer, keyLength C.size_t, outError **C.char) C.int {
	store, status := rawStore(handle, outError)
	if store == nil {
		return status
	}
	keyBytes, err := inputBytes(key, keyLength)
	if err != nil {
		return statusFor(err, outError)
	}
	if len(keyBytes) == 0 {
		return statusFor(fmt.Errorf("%w: key is required", kvlite.ErrInvalidArgument), outError)
	}
	return statusFor(store.Delete(context.Background(), keyBytes), outError)
}

//export kvlite_raw_scan_open
func kvlite_raw_scan_open(handle C.ulonglong, prefix unsafe.Pointer, prefixLength C.size_t, outCursor *C.ulonglong, outError **C.char) C.int {
	store, status := rawStore(handle, outError)
	if store == nil {
		return status
	}
	if outCursor == nil {
		return statusFor(fmt.Errorf("%w: output cursor is required", kvlite.ErrInvalidArgument), outError)
	}
	prefixBytes, err := inputBytes(prefix, prefixLength)
	if err != nil {
		return statusFor(err, outError)
	}
	snapshot := &scanSnapshot{}
	if err := store.ScanPrefix(context.Background(), prefixBytes, func(key, value []byte) error {
		snapshot.keys = append(snapshot.keys, append([]byte(nil), key...))
		snapshot.values = append(snapshot.values, append([]byte(nil), value...))
		return nil
	}); err != nil {
		return statusFor(err, outError)
	}
	cursors.Lock()
	cursor := cursors.next
	cursors.next++
	cursors.items[cursor] = snapshot
	cursors.Unlock()
	*outCursor = C.ulonglong(cursor)
	return statusOK
}

func lookupCursor(cursor C.ulonglong, out **C.char) *scanSnapshot {
	cursors.Lock()
	snapshot := cursors.items[uint64(cursor)]
	cursors.Unlock()
	if snapshot == nil {
		setError(out, fmt.Errorf("%w: invalid scan cursor %d", kvlite.ErrInvalidArgument, uint64(cursor)))
	}
	return snapshot
}

//export kvlite_raw_scan_next
func kvlite_raw_scan_next(cursor C.ulonglong, outKey *unsafe.Pointer, outKeyLength *C.size_t, outValue *unsafe.Pointer, outValueLength *C.size_t, outError **C.char) C.int {
	snapshot := lookupCursor(cursor, outError)
	if snapshot == nil {
		return statusInvalid
	}
	if outKey == nil || outKeyLength == nil || outValue == nil || outValueLength == nil {
		return statusFor(fmt.Errorf("%w: output key, value, and lengths are required", kvlite.ErrInvalidArgument), outError)
	}
	cursors.Lock()
	if snapshot.next >= len(snapshot.keys) {
		cursors.Unlock()
		// Exhaustion is not an error: NOT_FOUND with no error text tells the
		// host the snapshot is drained.
		return statusNotFound
	}
	key := snapshot.keys[snapshot.next]
	value := snapshot.values[snapshot.next]
	snapshot.next++
	cursors.Unlock()
	// The caller owns these allocations and releases them with kvlite_free.
	*outKey = C.CBytes(key)
	*outKeyLength = C.size_t(len(key))
	*outValue = C.CBytes(value)
	*outValueLength = C.size_t(len(value))
	return statusOK
}

//export kvlite_raw_scan_close
func kvlite_raw_scan_close(cursor C.ulonglong, outError **C.char) C.int {
	cursors.Lock()
	snapshot := cursors.items[uint64(cursor)]
	if snapshot != nil {
		delete(cursors.items, uint64(cursor))
	}
	cursors.Unlock()
	if snapshot == nil {
		return statusFor(fmt.Errorf("%w: invalid scan cursor %d", kvlite.ErrInvalidArgument, uint64(cursor)), outError)
	}
	return statusOK
}
