/*
 * Reference native KVLite driver module (test fixture).
 *
 * This is a complete, dependency-free C implementation of the
 * capi/kvlite_module.h contract: an in-memory storage driver with snapshot
 * scans, registered through kvlite_module_init_v1. It exists to prove the
 * native-module ABI is language-neutral (no Go toolchain required) and to
 * exercise the host loader end to end. It is not packaged or released.
 *
 * Build a loadable module with:
 *
 *   cc -shared -fPIC -o memdb.so memdb.c        (linux)
 *   cc -dynamiclib -fPIC -o memdb.dylib memdb.c (macOS)
 *   cl memdb.c -link -dll -out:memdb.dll        (windows, MSVC)
 */
#include <stdlib.h>
#include <string.h>

#include "../../kvlite_module.h"

typedef struct {
    unsigned char *key;
    size_t key_length;
    unsigned char *value;
    size_t value_length;
} memdb_pair;

typedef struct {
    memdb_pair *pairs;
    size_t count;
    size_t capacity;
    int live;
} memdb_db;

typedef struct {
    memdb_pair *pairs;
    size_t count;
    size_t next;
} memdb_cursor;

static memdb_db *databases = NULL;
static size_t database_count = 0;
static size_t database_capacity = 0;

static memdb_cursor **cursors = NULL;
static size_t cursor_count = 0;
static size_t cursor_capacity = 0;

static void memdb_set_error(char **out_error, const char *message) {
    if (out_error == NULL) {
        return;
    }
    *out_error = NULL;
    if (message == NULL || message[0] == '\0') {
        return;
    }
    size_t length = strlen(message);
    char *copy = (char *)malloc(length + 1);
    if (copy == NULL) {
        return;
    }
    memcpy(copy, message, length + 1);
    *out_error = copy;
}

static unsigned char *memdb_copy(const void *data, size_t length) {
    if (length == 0) {
        return NULL;
    }
    unsigned char *copy = (unsigned char *)malloc(length);
    if (copy == NULL) {
        return NULL;
    }
    memcpy(copy, data, length);
    return copy;
}

static memdb_db *memdb_lookup(unsigned long long handle) {
    if (handle == 0 || handle > database_count) {
        return NULL;
    }
    memdb_db *db = &databases[handle - 1];
    return db->live ? db : NULL;
}

static int memdb_open(const char *path, unsigned long long *out_handle, char **out_error) {
    if (path == NULL || path[0] == '\0' || out_handle == NULL) {
        memdb_set_error(out_error, "path and output handle are required");
        return 2;
    }
    if (database_count == database_capacity) {
        size_t next_capacity = database_capacity == 0 ? 4 : database_capacity * 2;
        memdb_db *next = (memdb_db *)realloc(databases, next_capacity * sizeof(*next));
        if (next == NULL) {
            memdb_set_error(out_error, "out of memory");
            return 3;
        }
        databases = next;
        database_capacity = next_capacity;
    }
    memdb_db *db = &databases[database_count];
    memset(db, 0, sizeof(*db));
    db->live = 1;
    *out_handle = (unsigned long long)(database_count + 1);
    database_count++;
    return 0;
}

static int memdb_close(unsigned long long handle, char **out_error) {
    memdb_db *db = memdb_lookup(handle);
    if (db == NULL) {
        memdb_set_error(out_error, "invalid database handle");
        return 2;
    }
    for (size_t i = 0; i < db->count; i++) {
        free(db->pairs[i].key);
        free(db->pairs[i].value);
    }
    free(db->pairs);
    memset(db, 0, sizeof(*db));
    return 0;
}

static int memdb_put(unsigned long long handle, const void *key, size_t key_length,
                     const void *value, size_t value_length, char **out_error) {
    memdb_db *db = memdb_lookup(handle);
    if (db == NULL) {
        memdb_set_error(out_error, "invalid database handle");
        return 2;
    }
    if (key == NULL || key_length == 0) {
        memdb_set_error(out_error, "key is required");
        return 2;
    }
    for (size_t i = 0; i < db->count; i++) {
        if (db->pairs[i].key_length == key_length && memcmp(db->pairs[i].key, key, key_length) == 0) {
            unsigned char *copy = memdb_copy(value, value_length);
            if (value_length > 0 && copy == NULL) {
                memdb_set_error(out_error, "out of memory");
                return 3;
            }
            free(db->pairs[i].value);
            db->pairs[i].value = copy;
            db->pairs[i].value_length = value_length;
            return 0;
        }
    }
    if (db->count == db->capacity) {
        size_t next_capacity = db->capacity == 0 ? 8 : db->capacity * 2;
        memdb_pair *next = (memdb_pair *)realloc(db->pairs, next_capacity * sizeof(*next));
        if (next == NULL) {
            memdb_set_error(out_error, "out of memory");
            return 3;
        }
        db->pairs = next;
        db->capacity = next_capacity;
    }
    unsigned char *key_copy = memdb_copy(key, key_length);
    unsigned char *value_copy = memdb_copy(value, value_length);
    if (key_copy == NULL || (value_length > 0 && value_copy == NULL)) {
        free(key_copy);
        free(value_copy);
        memdb_set_error(out_error, "out of memory");
        return 3;
    }
    db->pairs[db->count].key = key_copy;
    db->pairs[db->count].key_length = key_length;
    db->pairs[db->count].value = value_copy;
    db->pairs[db->count].value_length = value_length;
    db->count++;
    return 0;
}

static int memdb_get(unsigned long long handle, const void *key, size_t key_length,
                     void **out_value, size_t *out_length, char **out_error) {
    memdb_db *db = memdb_lookup(handle);
    if (db == NULL) {
        memdb_set_error(out_error, "invalid database handle");
        return 2;
    }
    if (key == NULL || key_length == 0 || out_value == NULL || out_length == NULL) {
        memdb_set_error(out_error, "key and outputs are required");
        return 2;
    }
    for (size_t i = 0; i < db->count; i++) {
        if (db->pairs[i].key_length == key_length && memcmp(db->pairs[i].key, key, key_length) == 0) {
            unsigned char *copy = memdb_copy(db->pairs[i].value, db->pairs[i].value_length);
            if (db->pairs[i].value_length > 0 && copy == NULL) {
                memdb_set_error(out_error, "out of memory");
                return 3;
            }
            *out_value = copy;
            *out_length = db->pairs[i].value_length;
            return 0;
        }
    }
    return 1;
}

static int memdb_delete(unsigned long long handle, const void *key, size_t key_length, char **out_error) {
    memdb_db *db = memdb_lookup(handle);
    if (db == NULL) {
        memdb_set_error(out_error, "invalid database handle");
        return 2;
    }
    if (key == NULL || key_length == 0) {
        memdb_set_error(out_error, "key is required");
        return 2;
    }
    for (size_t i = 0; i < db->count; i++) {
        if (db->pairs[i].key_length == key_length && memcmp(db->pairs[i].key, key, key_length) == 0) {
            free(db->pairs[i].key);
            free(db->pairs[i].value);
            db->pairs[i] = db->pairs[db->count - 1];
            db->count--;
            return 0;
        }
    }
    return 0;
}

static int memdb_scan_open(unsigned long long handle, const void *prefix, size_t prefix_length,
                           unsigned long long *out_cursor, char **out_error) {
    memdb_db *db = memdb_lookup(handle);
    if (db == NULL) {
        memdb_set_error(out_error, "invalid database handle");
        return 2;
    }
    if (out_cursor == NULL) {
        memdb_set_error(out_error, "output cursor is required");
        return 2;
    }
    memdb_cursor *cursor = (memdb_cursor *)calloc(1, sizeof(*cursor));
    if (cursor == NULL) {
        memdb_set_error(out_error, "out of memory");
        return 3;
    }
    for (size_t i = 0; i < db->count; i++) {
        if (prefix_length > 0 && (db->pairs[i].key_length < prefix_length ||
            memcmp(db->pairs[i].key, prefix, prefix_length) != 0)) {
            continue;
        }
        memdb_pair *slot = NULL;
        memdb_pair *grown = (memdb_pair *)realloc(cursor->pairs, (cursor->count + 1) * sizeof(*grown));
        if (grown == NULL) {
            goto fail;
        }
        cursor->pairs = grown;
        slot = &cursor->pairs[cursor->count];
        slot->key = memdb_copy(db->pairs[i].key, db->pairs[i].key_length);
        slot->value = memdb_copy(db->pairs[i].value, db->pairs[i].value_length);
        if (slot->key == NULL || (db->pairs[i].value_length > 0 && slot->value == NULL)) {
            free(slot->key);
            free(slot->value);
            goto fail;
        }
        slot->key_length = db->pairs[i].key_length;
        slot->value_length = db->pairs[i].value_length;
        cursor->count++;
    }
    if (cursor_count == cursor_capacity) {
        size_t next_capacity = cursor_capacity == 0 ? 4 : cursor_capacity * 2;
        memdb_cursor **next = (memdb_cursor **)realloc(cursors, next_capacity * sizeof(*next));
        if (next == NULL) {
            goto fail;
        }
        cursors = next;
        cursor_capacity = next_capacity;
    }
    cursors[cursor_count] = cursor;
    *out_cursor = (unsigned long long)(cursor_count + 1);
    cursor_count++;
    return 0;
fail:
    if (cursor != NULL) {
        for (size_t i = 0; i < cursor->count; i++) {
            free(cursor->pairs[i].key);
            free(cursor->pairs[i].value);
        }
        free(cursor->pairs);
        free(cursor);
    }
    memdb_set_error(out_error, "out of memory");
    return 3;
}

static memdb_cursor *memdb_cursor_lookup(unsigned long long cursor) {
    if (cursor == 0 || cursor > cursor_count) {
        return NULL;
    }
    return cursors[cursor - 1];
}

static int memdb_scan_next(unsigned long long cursor, void **out_key, size_t *out_key_length,
                           void **out_value, size_t *out_value_length, char **out_error) {
    memdb_cursor *snapshot = memdb_cursor_lookup(cursor);
    if (snapshot == NULL) {
        memdb_set_error(out_error, "invalid scan cursor");
        return 2;
    }
    if (out_key == NULL || out_key_length == NULL || out_value == NULL || out_value_length == NULL) {
        memdb_set_error(out_error, "outputs are required");
        return 2;
    }
    if (snapshot->next >= snapshot->count) {
        return 1;
    }
    memdb_pair *pair = &snapshot->pairs[snapshot->next];
    unsigned char *key_copy = memdb_copy(pair->key, pair->key_length);
    unsigned char *value_copy = memdb_copy(pair->value, pair->value_length);
    if (key_copy == NULL || (pair->value_length > 0 && value_copy == NULL)) {
        free(key_copy);
        free(value_copy);
        memdb_set_error(out_error, "out of memory");
        return 3;
    }
    snapshot->next++;
    *out_key = key_copy;
    *out_key_length = pair->key_length;
    *out_value = value_copy;
    *out_value_length = pair->value_length;
    return 0;
}

static int memdb_scan_close(unsigned long long cursor, char **out_error) {
    memdb_cursor *snapshot = memdb_cursor_lookup(cursor);
    if (snapshot == NULL) {
        memdb_set_error(out_error, "invalid scan cursor");
        return 2;
    }
    for (size_t i = 0; i < snapshot->count; i++) {
        free(snapshot->pairs[i].key);
        free(snapshot->pairs[i].value);
    }
    free(snapshot->pairs);
    free(snapshot);
    cursors[cursor - 1] = NULL;
    return 0;
}

static void memdb_free(void *pointer) {
    free(pointer);
}

static const kvlite_driver_ops_v1 memdb_ops = {
    memdb_open,
    memdb_close,
    memdb_put,
    memdb_get,
    memdb_delete,
    memdb_scan_open,
    memdb_scan_next,
    memdb_scan_close,
    memdb_free
};

static const kvlite_module_info_v1 memdb_info = {
    "memdb",
    "v0.1.0",
    "memdb",
    "embedded-storage"
};

int kvlite_module_init_v1(const kvlite_host_api_v1 *host, char **out_error) {
    if (host == NULL) {
        return 1;
    }
    if (host->host_abi != KVLITE_MODULE_ABI_VERSION) {
        if (out_error != NULL) {
            *out_error = NULL;
        }
        return 1;
    }
    if (host->register_driver == NULL) {
        return 1;
    }
    return host->register_driver(&memdb_info, &memdb_ops, out_error);
}
