/*
 * KVLite native-module ABI (v1): in-process driver extensions.
 *
 * A native module is a C shared library (or DLL/dylib) that exports one
 * well-known entry point, kvlite_module_init_v1. The host loads it with
 * dlopen/LoadLibrary, resolves that symbol, and calls it once with a table
 * of host services. The module registers one or more storage-driver
 * implementations by filling in an operation table; the host then drives the
 * module in-process with no subprocess and no second copy of the core.
 *
 * This is KVLite's answer to SQLite's sqlite3_extension_init: a small,
 * frozen C contract instead of a toolchain-coupled plugin system (Go's
 * plugin package is deliberately not supported: it ties modules to one exact
 * toolchain and build identity). Any language that can produce a C shared
 * library can implement a native module.
 *
 * Compatibility rules, frozen with this header:
 *
 * - KVLITE_MODULE_ABI_VERSION is incremented only for an incompatible
 *   init-contract change. Additive changes (for example, new optional
 *   operation-table members appended with documented defaults) keep v1.
 * - Status codes reuse capi/kvlite.h: 0 ok, 1 not found, 2 invalid argument,
 *   3 storage error. Scan exhaustion is reported as not found (1) with no
 *   error text, exactly like kvlite_raw_scan_next.
 * - Memory returned through out_value/out_key pointers is released with the
 *   module's own free function from the same operation table.
 * - Operation tables must stay valid for the process lifetime. The host
 *   never unloads a successfully initialized module.
 * - Engine keys and values are opaque bytes in the engine keyspace: no
 *   envelopes, no key mangling, no TTL synthesis. TTLs live in envelopes
 *   managed above the engine by the core.
 * - Scans snapshot at open; later writes are not visible through an open
 *   cursor. An empty prefix matches every key.
 */
#ifndef KVLITE_MODULE_H
#define KVLITE_MODULE_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Incremented only for an incompatible init-contract change. */
#define KVLITE_MODULE_ABI_VERSION 1

/* Well-known entry-point name a native module must export. */
#define KVLITE_MODULE_INIT_SYMBOL "kvlite_module_init_v1"

/* Storage-driver operation table provided by the module. */
typedef struct {
    int (*open)(const char *path, unsigned long long *out_handle, char **out_error);
    int (*close)(unsigned long long handle, char **out_error);
    int (*put)(unsigned long long handle,
               const void *key, size_t key_length,
               const void *value, size_t value_length,
               char **out_error);
    int (*get)(unsigned long long handle,
               const void *key, size_t key_length,
               void **out_value, size_t *out_length,
               char **out_error);
    int (*delete)(unsigned long long handle,
                  const void *key, size_t key_length,
                  char **out_error);
    int (*scan_open)(unsigned long long handle,
                     const void *prefix, size_t prefix_length,
                     unsigned long long *out_cursor,
                     char **out_error);
    int (*scan_next)(unsigned long long cursor,
                     void **out_key, size_t *out_key_length,
                     void **out_value, size_t *out_value_length,
                     char **out_error);
    int (*scan_close)(unsigned long long cursor, char **out_error);
    /* Release memory returned through out_error, out_value, or out_key. */
    void (*free)(void *pointer);
} kvlite_driver_ops_v1;

/* Module metadata recorded by the host on registration. */
typedef struct {
    /* Stable module name, e.g. "memdb". Canonical form: lowercase. */
    const char *name;
    /* Free-form version recorded in diagnostics, e.g. "v0.1.0". */
    const char *version;
    /* Driver name selected with WithDriver, e.g. "memdb". */
    const char *driver;
    /* Comma-separated capability tokens, e.g. "embedded-storage". May be NULL. */
    const char *capabilities;
} kvlite_module_info_v1;

/* Host services available to the module during initialization. */
typedef struct {
    /* Always KVLITE_MODULE_ABI_VERSION for a compatible host. */
    unsigned int host_abi;
    /*
     * Register one driver implementation. The info strings are copied by the
     * host; the ops table must stay valid for the process lifetime (the host
     * never unloads an initialized module). Returns 0 on success, non-zero
     * with *out_error set on failure (for example, a duplicate driver name).
     */
    int (*register_driver)(const kvlite_module_info_v1 *info,
                           const kvlite_driver_ops_v1 *ops,
                           char **out_error);
    /* Optional log sink; NULL when the host provides none. */
    void (*log)(const char *message);
} kvlite_host_api_v1;

/*
 * Module entry point. The host calls it exactly once after loading. Register
 * drivers through host->register_driver, then return 0. Return non-zero with
 * *out_error set to abort loading with that message.
 */
int kvlite_module_init_v1(const kvlite_host_api_v1 *host, char **out_error);

#ifdef __cplusplus
}
#endif

#endif
