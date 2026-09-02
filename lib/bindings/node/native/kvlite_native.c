#include <node_api.h>

#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#define strdup _strdup
#else
#include <dlfcn.h>
#endif

enum {
  KVLITE_OK = 0,
  KVLITE_NOT_FOUND = 1,
  KVLITE_INVALID_ARGUMENT = 2,
};

typedef unsigned int (*kvlite_abi_version_fn)(void);
typedef int (*kvlite_open_fn)(const char *, unsigned long long *, char **);
typedef int (*kvlite_close_fn)(unsigned long long, char **);
typedef int (*kvlite_put_fn)(unsigned long long, const void *, size_t, const void *, size_t, long long, char **);
typedef int (*kvlite_get_fn)(unsigned long long, const void *, size_t, void **, size_t *, char **);
typedef int (*kvlite_delete_fn)(unsigned long long, const void *, size_t, char **);
typedef void (*kvlite_free_fn)(void *);

typedef struct {
  bool loaded;
  char *path;
#ifdef _WIN32
  HMODULE library;
#else
  void *library;
#endif
  kvlite_abi_version_fn abi_version;
  kvlite_open_fn open;
  kvlite_close_fn close;
  kvlite_put_fn put;
  kvlite_get_fn get;
  kvlite_delete_fn delete_key;
  kvlite_free_fn free_pointer;
} kvlite_api;

static kvlite_api api = {0};

static napi_value throw_error(napi_env env, const char *code, const char *message) {
  napi_throw_error(env, code, message);
  return NULL;
}

static bool check(napi_env env, napi_status status, const char *message) {
  if (status == napi_ok) {
    return true;
  }
  napi_throw_error(env, "KVLITE_NAPI", message);
  return false;
}

static char *copy_string(napi_env env, napi_value value, const char *name) {
  size_t length = 0;
  napi_status status = napi_get_value_string_utf8(env, value, NULL, 0, &length);
  if (status != napi_ok) {
    char message[160];
    snprintf(message, sizeof(message), "%s must be a string", name);
    throw_error(env, "KVLITE_INVALID_ARGUMENT", message);
    return NULL;
  }
  char *result = malloc(length + 1);
  if (result == NULL) {
    throw_error(env, "KVLITE_STORAGE_ERROR", "Unable to allocate a KVLite argument");
    return NULL;
  }
  status = napi_get_value_string_utf8(env, value, result, length + 1, &length);
  if (status != napi_ok) {
    free(result);
    throw_error(env, "KVLITE_INVALID_ARGUMENT", "Unable to read a string argument");
    return NULL;
  }
  return result;
}

static bool read_handle(napi_env env, napi_value value, unsigned long long *out_handle) {
  bool lossless = false;
  if (!check(env, napi_get_value_bigint_uint64(env, value, out_handle, &lossless), "KVLite handle must be a bigint")) {
    return false;
  }
  if (!lossless) {
    throw_error(env, "KVLITE_INVALID_ARGUMENT", "KVLite handle is outside the uint64 range");
    return false;
  }
  return true;
}

static bool read_buffer(napi_env env, napi_value value, void **out_data, size_t *out_length, const char *name) {
  bool is_buffer = false;
  if (!check(env, napi_is_buffer(env, value, &is_buffer), "Unable to inspect a KVLite buffer")) {
    return false;
  }
  if (!is_buffer) {
    char message[160];
    snprintf(message, sizeof(message), "%s must be a Buffer", name);
    throw_error(env, "KVLITE_INVALID_ARGUMENT", message);
    return false;
  }
  if (!check(env, napi_get_buffer_info(env, value, out_data, out_length), "Unable to read a KVLite buffer")) {
    return false;
  }
  return true;
}

static napi_value throw_status(napi_env env, int status, char *native_error) {
  const char *message = native_error == NULL ? "KVLite native operation failed" : native_error;
  const char *code = "KVLITE_STORAGE_ERROR";
  if (status == KVLITE_NOT_FOUND) {
    code = "KVLITE_NOT_FOUND";
  } else if (status == KVLITE_INVALID_ARGUMENT) {
    code = "KVLITE_INVALID_ARGUMENT";
  }
  napi_throw_error(env, code, message);
  if (native_error != NULL && api.free_pointer != NULL) {
    api.free_pointer(native_error);
  }
  return NULL;
}

#ifdef _WIN32
static HMODULE load_library(const char *path) {
  return LoadLibraryA(path);
}

static void *load_symbol(HMODULE library, const char *name) {
  return (void *)GetProcAddress(library, name);
}

static const char *load_error(void) {
  return "Windows could not load the KVLite shared library";
}
#else
static void *load_library(const char *path) {
  return dlopen(path, RTLD_NOW | RTLD_LOCAL);
}

static void *load_symbol(void *library, const char *name) {
  return dlsym(library, name);
}

static const char *load_error(void) {
  const char *error = dlerror();
  return error == NULL ? "dynamic loader error" : error;
}
#endif

static bool load_api(napi_env env, const char *path) {
  if (api.loaded) {
    if (strcmp(api.path, path) == 0) {
      return true;
    }
    throw_error(env, "KVLITE_NATIVE_LIBRARY", "Only one libkvlite path can be loaded in a Node.js process");
    return false;
  }

  api.library = load_library(path);
  if (api.library == NULL) {
    char message[1024];
    snprintf(message, sizeof(message), "Unable to load libkvlite at %s: %s", path, load_error());
    throw_error(env, "KVLITE_NATIVE_LIBRARY", message);
    return false;
  }

#define RESOLVE(field, symbol, type) do { \
  api.field = (type)load_symbol(api.library, symbol); \
  if (api.field == NULL) { \
    char message[512]; \
    snprintf(message, sizeof(message), "libkvlite is missing required symbol %s", symbol); \
    throw_error(env, "KVLITE_NATIVE_LIBRARY", message); \
    return false; \
  } \
} while (0)
  RESOLVE(abi_version, "kvlite_abi_version", kvlite_abi_version_fn);
  RESOLVE(open, "kvlite_open", kvlite_open_fn);
  RESOLVE(close, "kvlite_close", kvlite_close_fn);
  RESOLVE(put, "kvlite_put", kvlite_put_fn);
  RESOLVE(get, "kvlite_get", kvlite_get_fn);
  RESOLVE(delete_key, "kvlite_delete", kvlite_delete_fn);
  RESOLVE(free_pointer, "kvlite_free", kvlite_free_fn);
#undef RESOLVE

  if (api.abi_version() != 1) {
    throw_error(env, "KVLITE_NATIVE_LIBRARY", "KVLite native ABI mismatch: this Node.js package needs ABI 1");
    return false;
  }
  api.path = strdup(path);
  if (api.path == NULL) {
    throw_error(env, "KVLITE_STORAGE_ERROR", "Unable to retain the KVLite library path");
    return false;
  }
  api.loaded = true;
  return true;
}

static napi_value native_open(napi_env env, napi_callback_info info) {
  size_t argc = 2;
  napi_value args[2];
  if (!check(env, napi_get_cb_info(env, info, &argc, args, NULL, NULL), "Unable to read KVLite arguments")) {
    return NULL;
  }
  if (argc != 2) {
    return throw_error(env, "KVLITE_INVALID_ARGUMENT", "open requires database path and libkvlite path");
  }
  char *path = copy_string(env, args[0], "database path");
  char *library_path = copy_string(env, args[1], "libkvlite path");
  if (path == NULL || library_path == NULL) {
    free(path);
    free(library_path);
    return NULL;
  }
  if (!load_api(env, library_path)) {
    free(path);
    free(library_path);
    return NULL;
  }
  unsigned long long handle = 0;
  char *native_error = NULL;
  int status = api.open(path, &handle, &native_error);
  free(path);
  free(library_path);
  if (status != KVLITE_OK) {
    return throw_status(env, status, native_error);
  }
  napi_value result;
  if (!check(env, napi_create_bigint_uint64(env, handle, &result), "Unable to create a KVLite handle")) {
    return NULL;
  }
  return result;
}

static napi_value native_close(napi_env env, napi_callback_info info) {
  size_t argc = 1;
  napi_value args[1];
  if (!check(env, napi_get_cb_info(env, info, &argc, args, NULL, NULL), "Unable to read KVLite arguments")) {
    return NULL;
  }
  if (argc != 1 || !api.loaded) {
    return throw_error(env, "KVLITE_INVALID_ARGUMENT", "close requires an open KVLite handle");
  }
  unsigned long long handle;
  if (!read_handle(env, args[0], &handle)) {
    return NULL;
  }
  char *native_error = NULL;
  int status = api.close(handle, &native_error);
  if (status != KVLITE_OK) {
    return throw_status(env, status, native_error);
  }
  napi_value result;
  napi_get_undefined(env, &result);
  return result;
}

static napi_value native_put(napi_env env, napi_callback_info info) {
  size_t argc = 4;
  napi_value args[4];
  if (!check(env, napi_get_cb_info(env, info, &argc, args, NULL, NULL), "Unable to read KVLite arguments")) {
    return NULL;
  }
  if (argc != 4 || !api.loaded) {
    return throw_error(env, "KVLITE_INVALID_ARGUMENT", "put requires handle, key, value, and TTL");
  }
  unsigned long long handle;
  void *key;
  void *value;
  size_t key_length;
  size_t value_length;
  long long ttl_seconds;
  if (!read_handle(env, args[0], &handle) || !read_buffer(env, args[1], &key, &key_length, "key") ||
      !read_buffer(env, args[2], &value, &value_length, "value") ||
      !check(env, napi_get_value_int64(env, args[3], &ttl_seconds), "TTL must be an integer")) {
    return NULL;
  }
  char *native_error = NULL;
  int status = api.put(handle, key, key_length, value, value_length, ttl_seconds, &native_error);
  if (status != KVLITE_OK) {
    return throw_status(env, status, native_error);
  }
  napi_value result;
  napi_get_undefined(env, &result);
  return result;
}

static napi_value native_get(napi_env env, napi_callback_info info) {
  size_t argc = 2;
  napi_value args[2];
  if (!check(env, napi_get_cb_info(env, info, &argc, args, NULL, NULL), "Unable to read KVLite arguments")) {
    return NULL;
  }
  if (argc != 2 || !api.loaded) {
    return throw_error(env, "KVLITE_INVALID_ARGUMENT", "get requires handle and key");
  }
  unsigned long long handle;
  void *key;
  size_t key_length;
  if (!read_handle(env, args[0], &handle) || !read_buffer(env, args[1], &key, &key_length, "key")) {
    return NULL;
  }
  void *value = NULL;
  size_t value_length = 0;
  char *native_error = NULL;
  int status = api.get(handle, key, key_length, &value, &value_length, &native_error);
  if (status != KVLITE_OK) {
    return throw_status(env, status, native_error);
  }
  napi_value result;
  napi_status napi_result = napi_create_buffer_copy(env, value_length, value, NULL, &result);
  if (value != NULL) {
    api.free_pointer(value);
  }
  if (!check(env, napi_result, "Unable to copy a KVLite value")) {
    return NULL;
  }
  return result;
}

static napi_value native_delete(napi_env env, napi_callback_info info) {
  size_t argc = 2;
  napi_value args[2];
  if (!check(env, napi_get_cb_info(env, info, &argc, args, NULL, NULL), "Unable to read KVLite arguments")) {
    return NULL;
  }
  if (argc != 2 || !api.loaded) {
    return throw_error(env, "KVLITE_INVALID_ARGUMENT", "delete requires handle and key");
  }
  unsigned long long handle;
  void *key;
  size_t key_length;
  if (!read_handle(env, args[0], &handle) || !read_buffer(env, args[1], &key, &key_length, "key")) {
    return NULL;
  }
  char *native_error = NULL;
  int status = api.delete_key(handle, key, key_length, &native_error);
  if (status != KVLITE_OK) {
    return throw_status(env, status, native_error);
  }
  napi_value result;
  napi_get_undefined(env, &result);
  return result;
}

static napi_value init(napi_env env, napi_value exports) {
  napi_property_descriptor properties[] = {
      {"open", NULL, native_open, NULL, NULL, NULL, napi_default, NULL},
      {"close", NULL, native_close, NULL, NULL, NULL, napi_default, NULL},
      {"put", NULL, native_put, NULL, NULL, NULL, napi_default, NULL},
      {"get", NULL, native_get, NULL, NULL, NULL, napi_default, NULL},
      {"delete", NULL, native_delete, NULL, NULL, NULL, napi_default, NULL},
  };
  if (!check(env, napi_define_properties(env, exports, sizeof(properties) / sizeof(properties[0]), properties), "Unable to export KVLite native functions")) {
    return NULL;
  }
  return exports;
}

NAPI_MODULE(NODE_GYP_MODULE_NAME, init)
