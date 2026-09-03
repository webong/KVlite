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
 * Open a KVLite directory through the bundle's default driver. A driver bundle
 * registers exactly the drivers it ships; use kvlite_open_with_driver to name
 * one explicitly. On failure, *out_error receives a malloc'd message released
 * by kvlite_free.
 */
int kvlite_open(const char *path, unsigned long long *out_handle, char **out_error);

/*
 * Open a KVLite directory through one explicit driver name, such as
 * "rocksdb", "leveldb", or a separately installed driver such as
 * "berkeleydb". This is an additive ABI v1 symbol: callers that
 * need to work with an older v1 library should keep using kvlite_open for the
 * default driver. A database directory records its selected driver and is
 * never interchangeable with a directory from another driver.
 * On failure, *out_error receives a malloc'd message released by kvlite_free.
 */
int kvlite_open_with_backend(const char *path, const char *backend,
                             unsigned long long *out_handle, char **out_error);

/* Preferred spelling for kvlite_open_with_backend. Both remain ABI v1. */
int kvlite_open_with_driver(const char *path, const char *driver,
                            unsigned long long *out_handle, char **out_error);

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
