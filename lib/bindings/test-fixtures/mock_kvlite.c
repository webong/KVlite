/*
 * Small ABI-compatible test double used by the language binding suites.
 * It deliberately has no RocksDB dependency: the bindings can verify pointer
 * ownership, status handling, and JSON serialization on any developer host.
 */
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>

enum {
    KVLITE_OK = 0,
    KVLITE_NOT_FOUND = 1,
    KVLITE_INVALID_ARGUMENT = 2,
    KVLITE_STORAGE_ERROR = 3
};

static unsigned char *stored_key;
static size_t stored_key_length;
static unsigned char *stored_value;
static size_t stored_value_length;
static int is_open;

static void set_error(char **out_error, const char *message) {
    size_t length;
    if (out_error == NULL) {
        return;
    }
    length = strlen(message) + 1;
    *out_error = malloc(length);
    if (*out_error != NULL) {
        memcpy(*out_error, message, length);
    }
}

static int valid_handle(unsigned long long handle, char **out_error) {
    if (!is_open || handle != 1) {
        set_error(out_error, "invalid database handle");
        return 0;
    }
    return 1;
}

unsigned int kvlite_abi_version(void) {
    return 1;
}

int kvlite_open(const char *path, unsigned long long *out_handle, char **out_error) {
    if (path == NULL || path[0] == '\0' || out_handle == NULL) {
        set_error(out_error, "path and output handle are required");
        return KVLITE_INVALID_ARGUMENT;
    }
    is_open = 1;
    *out_handle = 1;
    return KVLITE_OK;
}

#ifndef KVLITE_MOCK_LEGACY_ABI
int kvlite_open_with_backend(const char *path, const char *backend,
                             unsigned long long *out_handle, char **out_error) {
    if (backend == NULL || backend[0] == '\0') {
        set_error(out_error, "backend is required");
        return KVLITE_INVALID_ARGUMENT;
    }
    return kvlite_open(path, out_handle, out_error);
}
#endif

int kvlite_close(unsigned long long handle, char **out_error) {
    if (!valid_handle(handle, out_error)) {
        return KVLITE_INVALID_ARGUMENT;
    }
    is_open = 0;
    free(stored_key);
    free(stored_value);
    stored_key = NULL;
    stored_value = NULL;
    stored_key_length = 0;
    stored_value_length = 0;
    return KVLITE_OK;
}

int kvlite_put(unsigned long long handle,
               const void *key, size_t key_length,
               const void *value, size_t value_length,
               long long ttl_seconds,
               char **out_error) {
    unsigned char *new_key;
    unsigned char *new_value;
    (void)ttl_seconds;
    if (!valid_handle(handle, out_error)) {
        return KVLITE_INVALID_ARGUMENT;
    }
    if (key == NULL || key_length == 0 || (value == NULL && value_length != 0)) {
        set_error(out_error, "key is required");
        return KVLITE_INVALID_ARGUMENT;
    }
    new_key = malloc(key_length);
    new_value = malloc(value_length == 0 ? 1 : value_length);
    if (new_key == NULL || new_value == NULL) {
        free(new_key);
        free(new_value);
        set_error(out_error, "out of memory");
        return KVLITE_STORAGE_ERROR;
    }
    memcpy(new_key, key, key_length);
    if (value_length != 0) {
        memcpy(new_value, value, value_length);
    }
    free(stored_key);
    free(stored_value);
    stored_key = new_key;
    stored_key_length = key_length;
    stored_value = new_value;
    stored_value_length = value_length;
    return KVLITE_OK;
}

int kvlite_get(unsigned long long handle,
               const void *key, size_t key_length,
               void **out_value, size_t *out_length,
               char **out_error) {
    unsigned char *copy;
    if (!valid_handle(handle, out_error)) {
        return KVLITE_INVALID_ARGUMENT;
    }
    if (key == NULL || key_length == 0 || out_value == NULL || out_length == NULL) {
        set_error(out_error, "key and output value are required");
        return KVLITE_INVALID_ARGUMENT;
    }
    if (stored_key == NULL || stored_key_length != key_length || memcmp(stored_key, key, key_length) != 0) {
        set_error(out_error, "not found");
        return KVLITE_NOT_FOUND;
    }
    copy = malloc(stored_value_length == 0 ? 1 : stored_value_length);
    if (copy == NULL) {
        set_error(out_error, "out of memory");
        return KVLITE_STORAGE_ERROR;
    }
    if (stored_value_length != 0) {
        memcpy(copy, stored_value, stored_value_length);
    }
    *out_value = copy;
    *out_length = stored_value_length;
    return KVLITE_OK;
}

int kvlite_delete(unsigned long long handle,
                  const void *key, size_t key_length,
                  char **out_error) {
    if (!valid_handle(handle, out_error)) {
        return KVLITE_INVALID_ARGUMENT;
    }
    if (key == NULL || key_length == 0) {
        set_error(out_error, "key is required");
        return KVLITE_INVALID_ARGUMENT;
    }
    if (stored_key != NULL && stored_key_length == key_length && memcmp(stored_key, key, key_length) == 0) {
        free(stored_key);
        free(stored_value);
        stored_key = NULL;
        stored_value = NULL;
        stored_key_length = 0;
        stored_value_length = 0;
    }
    return KVLITE_OK;
}

void kvlite_free(void *pointer) {
    free(pointer);
}
