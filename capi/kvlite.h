#ifndef KVLITE_H
#define KVLITE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Incremented only for an incompatible C ABI change. */
#define KVLITE_ABI_VERSION 1

enum {
    KVLITE_OK = 0,
    KVLITE_NOT_FOUND = 1,
    KVLITE_INVALID_ARGUMENT = 2,
    KVLITE_STORAGE_ERROR = 3
};

/* Return the ABI implemented by this shared library. */
unsigned int kvlite_abi_version(void);

/*
 * Open a RocksDB directory. Build the shared library with the rocksdb tag.
 * On failure, *out_error receives a malloc'd message released by kvlite_free.
 */
int kvlite_open(const char *path, unsigned long long *out_handle, char **out_error);

/* Close an owner handle. Closing invalidates the handle. */
int kvlite_close(unsigned long long handle, char **out_error);

/* Store arbitrary serialized bytes. ttl_seconds == 0 means no expiry. */
int kvlite_put(unsigned long long handle,
               const void *key, size_t key_length,
               const void *value, size_t value_length,
               long long ttl_seconds,
               char **out_error);

/* Return an allocated serialized payload. Release it with kvlite_free. */
int kvlite_get(unsigned long long handle,
               const void *key, size_t key_length,
               void **out_value, size_t *out_length,
               char **out_error);

/* Delete a key. Deleting a missing key succeeds. */
int kvlite_delete(unsigned long long handle,
                  const void *key, size_t key_length,
                  char **out_error);

/* Release memory returned through out_error or out_value. */
void kvlite_free(void *pointer);

#ifdef __cplusplus
}
#endif

#endif
