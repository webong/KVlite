//go:build berkeleydb && cgo

package berkeleydb

/*
#cgo LDFLAGS: -ldb
#include <db.h>

// macOS still ships a legacy db.h that is not Oracle Berkeley DB. Require the
// C API surface this adapter uses, rather than one exact Berkeley DB release.
#if !defined(DB_VERSION_MAJOR) || !defined(DB_VERSION_MINOR) || \
	!defined(DB_BTREE) || !defined(DB_CREATE) || !defined(DB_THREAD) || \
	!defined(DB_SET_RANGE) || !defined(DB_NOTFOUND)
#error "KVLite Berkeley DB requires a compatible modern Berkeley DB C API; set CGO_CFLAGS and CGO_LDFLAGS to the intended Berkeley DB installation"
#endif

#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum { kvlite_bdb_notfound = DB_NOTFOUND };

static int kvlite_bdb_dbt(DBT *dbt, const void *data, size_t length) {
	if (length > UINT32_MAX) {
		return EINVAL;
	}
	memset(dbt, 0, sizeof(*dbt));
	dbt->data = (void *)data;
	dbt->size = (u_int32_t)length;
	return 0;
}

static int kvlite_bdb_copy_dbt(const DBT *source, void **out, size_t *out_length) {
	void *copy;

	if (source == NULL || out == NULL || out_length == NULL) {
		return EINVAL;
	}
	*out = NULL;
	*out_length = 0;
	if (source->size == 0) {
		return 0;
	}
	copy = malloc(source->size);
	if (copy == NULL) {
		return ENOMEM;
	}
	memcpy(copy, source->data, source->size);
	*out = copy;
	*out_length = source->size;
	return 0;
}

static int kvlite_bdb_copy_pair(const DBT *key, const DBT *value,
	void **out_key, size_t *out_key_length,
	void **out_value, size_t *out_value_length) {
	int status;

	status = kvlite_bdb_copy_dbt(key, out_key, out_key_length);
	if (status != 0) {
		return status;
	}
	status = kvlite_bdb_copy_dbt(value, out_value, out_value_length);
	if (status != 0) {
		free(*out_key);
		*out_key = NULL;
		*out_key_length = 0;
	}
	return status;
}

static int kvlite_bdb_open(const char *path, DB **out) {
	DB *db = NULL;
	int status;

	if (path == NULL || out == NULL) {
		return EINVAL;
	}
	*out = NULL;
	status = db_create(&db, NULL, 0);
	if (status != 0) {
		return status;
	}
	status = db->open(db, NULL, path, NULL, DB_BTREE, DB_CREATE | DB_THREAD, 0600);
	if (status != 0) {
		(void)db->close(db, 0);
		return status;
	}
	*out = db;
	return 0;
}

static int kvlite_bdb_close(DB *db) {
	if (db == NULL) {
		return 0;
	}
	return db->close(db, 0);
}

static int kvlite_bdb_get(DB *db, const void *key_data, size_t key_length,
	void **out_value, size_t *out_value_length) {
	DBT key;
	DBT value;
	int status;

	if (db == NULL) {
		return EINVAL;
	}
	status = kvlite_bdb_dbt(&key, key_data, key_length);
	if (status != 0) {
		return status;
	}
	memset(&value, 0, sizeof(value));
	status = db->get(db, NULL, &key, &value, 0);
	if (status != 0) {
		return status;
	}
	return kvlite_bdb_copy_dbt(&value, out_value, out_value_length);
}

static int kvlite_bdb_put(DB *db, const void *key_data, size_t key_length,
	const void *value_data, size_t value_length) {
	DBT key;
	DBT value;
	int status;

	if (db == NULL) {
		return EINVAL;
	}
	status = kvlite_bdb_dbt(&key, key_data, key_length);
	if (status != 0) {
		return status;
	}
	status = kvlite_bdb_dbt(&value, value_data, value_length);
	if (status != 0) {
		return status;
	}
	return db->put(db, NULL, &key, &value, 0);
}

static int kvlite_bdb_delete(DB *db, const void *key_data, size_t key_length) {
	DBT key;
	int status;

	if (db == NULL) {
		return EINVAL;
	}
	status = kvlite_bdb_dbt(&key, key_data, key_length);
	if (status != 0) {
		return status;
	}
	return db->del(db, NULL, &key, 0);
}

static int kvlite_bdb_cursor_open(DB *db, DBC **out) {
	if (db == NULL || out == NULL) {
		return EINVAL;
	}
	*out = NULL;
	return db->cursor(db, NULL, out, 0);
}

static int kvlite_bdb_cursor_close(DBC *cursor) {
	if (cursor == NULL) {
		return 0;
	}
	return cursor->close(cursor);
}

static int kvlite_bdb_cursor_read(DBC *cursor, const void *seek_data,
	size_t seek_length, int seek, void **out_key, size_t *out_key_length,
	void **out_value, size_t *out_value_length) {
	DBT key;
	DBT value;
	int status;

	if (cursor == NULL) {
		return EINVAL;
	}
	memset(&key, 0, sizeof(key));
	memset(&value, 0, sizeof(value));
	if (seek) {
		status = kvlite_bdb_dbt(&key, seek_data, seek_length);
		if (status != 0) {
			return status;
		}
		status = cursor->get(cursor, &key, &value, DB_SET_RANGE);
	} else {
		status = cursor->get(cursor, &key, &value, DB_NEXT);
	}
	if (status != 0) {
		return status;
	}
	return kvlite_bdb_copy_pair(&key, &value, out_key, out_key_length,
		out_value, out_value_length);
}

static const char *kvlite_bdb_error(int status) {
	return db_strerror(status);
}
*/
import "C"

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"unsafe"

	"github.com/webong/kvlite"
)

func nativeAvailable() error { return nil }

type engine struct {
	db *C.DB
}

func openNative(path string, _ kvlite.DriverOptions) (kvlite.Engine, error) {
	filename := filepath.Join(path, DatabaseFilename)
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))

	var database *C.DB
	if status := C.kvlite_bdb_open(cFilename, &database); status != 0 {
		return nil, bdbError("open", status)
	}
	return &engine{db: database}, nil
}

func (engine *engine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	var value unsafe.Pointer
	var length C.size_t
	status := C.kvlite_bdb_get(engine.db, bytePointer(key), C.size_t(len(key)), &value, &length)
	if status == C.kvlite_bdb_notfound {
		return nil, false, nil
	}
	if status != 0 {
		return nil, false, bdbError("get", status)
	}
	result, err := takeCBytes(value, length)
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

func (engine *engine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if status := C.kvlite_bdb_put(engine.db, bytePointer(key), C.size_t(len(key)), bytePointer(value), C.size_t(len(value))); status != 0 {
		return bdbError("put", status)
	}
	return nil
}

func (engine *engine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status := C.kvlite_bdb_delete(engine.db, bytePointer(key), C.size_t(len(key)))
	if status == 0 || status == C.kvlite_bdb_notfound {
		return nil
	}
	return bdbError("delete", status)
}

func (engine *engine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	var cursor *C.DBC
	if status := C.kvlite_bdb_cursor_open(engine.db, &cursor); status != 0 {
		return bdbError("open cursor", status)
	}
	defer func() {
		if status := C.kvlite_bdb_cursor_close(cursor); err == nil && status != 0 {
			err = bdbError("close cursor", status)
		}
	}()

	seek := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var keyPointer unsafe.Pointer
		var keyLength C.size_t
		var valuePointer unsafe.Pointer
		var valueLength C.size_t
		status := C.kvlite_bdb_cursor_read(
			cursor,
			bytePointer(prefix),
			C.size_t(len(prefix)),
			boolToCInt(seek),
			&keyPointer,
			&keyLength,
			&valuePointer,
			&valueLength,
		)
		if status == C.kvlite_bdb_notfound {
			return nil
		}
		if status != 0 {
			return bdbError("scan", status)
		}
		key, value, err := takeCPair(keyPointer, keyLength, valuePointer, valueLength)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(key, prefix) {
			return nil
		}
		if err := callback(key, value); err != nil {
			return err
		}
		seek = false
	}
}

func (engine *engine) Close() error {
	status := C.kvlite_bdb_close(engine.db)
	engine.db = nil
	if status != 0 {
		return bdbError("close", status)
	}
	return nil
}

func bytePointer(value []byte) unsafe.Pointer {
	if len(value) == 0 {
		return nil
	}
	return unsafe.Pointer(unsafe.SliceData(value))
}

func boolToCInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func takeCPair(keyPointer unsafe.Pointer, keyLength C.size_t, valuePointer unsafe.Pointer, valueLength C.size_t) ([]byte, []byte, error) {
	key, err := takeCBytes(keyPointer, keyLength)
	if err != nil {
		if valuePointer != nil {
			C.free(valuePointer)
		}
		return nil, nil, err
	}
	value, err := takeCBytes(valuePointer, valueLength)
	if err != nil {
		return nil, nil, err
	}
	return key, value, nil
}

func takeCBytes(pointer unsafe.Pointer, length C.size_t) ([]byte, error) {
	if pointer == nil {
		if length == 0 {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("kvlite: Berkeley DB returned a nil value with length %d", length)
	}
	defer C.free(pointer)
	maxInt := uint64(^uint(0) >> 1)
	if uint64(length) > maxInt {
		return nil, fmt.Errorf("kvlite: Berkeley DB value is too large for this process: %d bytes", length)
	}
	return append([]byte(nil), unsafe.Slice((*byte)(pointer), int(length))...), nil
}

func bdbError(operation string, status C.int) error {
	message := C.GoString(C.kvlite_bdb_error(status))
	return fmt.Errorf("kvlite: Berkeley DB %s: %s (status %d)", operation, message, int(status))
}
