//go:build cgo

package kvlite

/*
#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

typedef unsigned long long kvlite_handle_t;

typedef int (*kvlite_abi_version_fn)(void);
typedef int (*kvlite_open_with_driver_fn)(const char *, const char *, kvlite_handle_t *, char **);
typedef int (*kvlite_open_with_backend_fn)(const char *, const char *, kvlite_handle_t *, char **);
typedef int (*kvlite_open_fn)(const char *, kvlite_handle_t *, char **);
typedef int (*kvlite_close_fn)(kvlite_handle_t, char **);
typedef int (*kvlite_put_fn)(kvlite_handle_t, const void *, size_t, const void *, size_t, long long, char **);
typedef int (*kvlite_get_fn)(kvlite_handle_t, const void *, size_t, void **, size_t *, char **);
typedef int (*kvlite_delete_fn)(kvlite_handle_t, const void *, size_t, char **);
typedef void (*kvlite_free_fn)(void *);

typedef struct {
	void *library;
	kvlite_abi_version_fn abi_version;
	kvlite_open_with_driver_fn open_with_driver;
	kvlite_open_with_backend_fn open_with_backend;
	kvlite_open_fn open;
	kvlite_close_fn close;
	kvlite_put_fn put;
	kvlite_get_fn get;
	kvlite_delete_fn delete;
	kvlite_free_fn free_ptr;
} kvlite_module_api;

static void set_loader_error(char **out, const char *message) {
	if (out == NULL) {
		return;
	}
	*out = NULL;
		if (message == NULL || message[0] == '\0') {
		return;
	}
	size_t length = strlen(message);
	char *allocated = (char *)malloc(length + 1);
	if (allocated == NULL) {
		return;
	}
	memcpy(allocated, message, length + 1);
	*out = allocated;
}

static const char *platform_error(void) {
#ifdef _WIN32
	return "unable to load KVLite shared library";
#else
	const char *error = dlerror();
	return error == NULL ? "dynamic loader error" : error;
#endif
}

static void *kvlite_load_library(const char *path) {
#ifdef _WIN32
	return (void *)LoadLibraryA(path);
#else
	return dlopen(path, RTLD_NOW | RTLD_LOCAL);
#endif
}

static void *kvlite_resolve_symbol(void *library, const char *name) {
#ifdef _WIN32
	return (void *)GetProcAddress((HMODULE)library, name);
#else
	return dlsym(library, name);
#endif
}

static void kvlite_unload_library(void *library) {
#ifdef _WIN32
	FreeLibrary((HMODULE)library);
#else
	dlclose(library);
#endif
}

static void kvlite_module_unload(kvlite_module_api *api) {
	if (api == NULL || api->library == NULL) {
		return;
	}
	kvlite_unload_library(api->library);
	api->library = NULL;
}

static int kvlite_module_load(const char *path, kvlite_module_api *api, char **out_error) {
		if (path == NULL || path[0] == '\0') {
		set_loader_error(out_error, "library path is required");
		return 1;
	}
	if (api == NULL) {
		set_loader_error(out_error, "loader state is missing");
		return 1;
	}
	memset(api, 0, sizeof(*api));
	api->library = kvlite_load_library(path);
	if (api->library == NULL) {
		set_loader_error(out_error, platform_error());
		return 1;
	}

#define RESOLVE(field, symbol, type) do {                                              \
	api->field = (type)kvlite_resolve_symbol(api->library, symbol);                    \
	if (api->field == NULL) {                                                           \
		kvlite_module_unload(api);                                                      \
		set_loader_error(out_error, "library is missing required symbol '" symbol "'");   \
		return 1;                                                                       \
	}                                                                                    \
} while (0)

	RESOLVE(abi_version, "kvlite_abi_version", kvlite_abi_version_fn);
	RESOLVE(close, "kvlite_close", kvlite_close_fn);
	RESOLVE(put, "kvlite_put", kvlite_put_fn);
	RESOLVE(get, "kvlite_get", kvlite_get_fn);
	RESOLVE(delete, "kvlite_delete", kvlite_delete_fn);
	RESOLVE(free_ptr, "kvlite_free", kvlite_free_fn);

#undef RESOLVE

	api->open_with_driver = (kvlite_open_with_driver_fn)kvlite_resolve_symbol(api->library, "kvlite_open_with_driver");
	api->open_with_backend = (kvlite_open_with_backend_fn)kvlite_resolve_symbol(api->library, "kvlite_open_with_backend");
	api->open = (kvlite_open_fn)kvlite_resolve_symbol(api->library, "kvlite_open");

	if (api->open_with_driver == NULL && api->open_with_backend == NULL && api->open == NULL) {
		kvlite_module_unload(api);
		set_loader_error(out_error, "library has no usable open() function");
		return 1;
	}

	return 0;
}

static int kvlite_module_abi_version(const kvlite_module_api *api, unsigned int *out_version, char **out_error) {
	if (api == NULL || out_version == NULL) {
		return 1;
	}
	if (api->abi_version == NULL) {
		set_loader_error(out_error, "library is missing kvlite_abi_version");
		return 1;
	}
	*out_version = api->abi_version();
	return 0;
}

static int kvlite_module_open(const kvlite_module_api *api, const char *path, const char *driver, kvlite_handle_t *out_handle, char **out_error) {
	if (api == NULL) {
		return 1;
	}
	if (api->open_with_driver != NULL) {
		return api->open_with_driver(path, driver, out_handle, out_error);
	}
	if (api->open_with_backend != NULL) {
		return api->open_with_backend(path, driver, out_handle, out_error);
	}
	if (api->open == NULL) {
		set_loader_error(out_error, "library is missing a usable open function");
		return 1;
	}
	return api->open(path, out_handle, out_error);
}

static int kvlite_module_close(const kvlite_module_api *api, kvlite_handle_t handle, char **out_error) {
	if (api == NULL || api->close == NULL) {
		return 1;
	}
	return api->close(handle, out_error);
}

static int kvlite_module_put(const kvlite_module_api *api, kvlite_handle_t handle, const void *key, size_t key_length, const void *value, size_t value_length, long long ttl_seconds, char **out_error) {
	if (api == NULL || api->put == NULL) {
		return 1;
	}
	return api->put(handle, key, key_length, value, value_length, ttl_seconds, out_error);
}

static int kvlite_module_get(const kvlite_module_api *api, kvlite_handle_t handle, const void *key, size_t key_length, void **out_value, size_t *out_length, char **out_error) {
	if (api == NULL || api->get == NULL) {
		return 1;
	}
	return api->get(handle, key, key_length, out_value, out_length, out_error);
}

static int kvlite_module_delete(const kvlite_module_api *api, kvlite_handle_t handle, const void *key, size_t key_length, char **out_error) {
	if (api == NULL || api->delete == NULL) {
		return 1;
	}
	return api->delete(handle, key, key_length, out_error);
}

static void kvlite_module_free(const kvlite_module_api *api, void *pointer) {
	if (api == NULL || api->free_ptr == NULL || pointer == NULL) {
		return;
	}
	api->free_ptr(pointer);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"unsafe"
)

type moduleLibrary struct {
	state *C.kvlite_module_api
}

func openModuleDriverFromArtifact(path string, module Module, options DriverOptions) (Engine, error) {
	_ = options

	artifact, err := module.ArtifactForCurrentPlatform(ModuleArtifactCShared)
	if err != nil {
		return nil, fmt.Errorf("kvlite: open module artifact: %w", err)
	}
	artifactPath, err := module.ArtifactPath(artifact)
	if err != nil {
		return nil, fmt.Errorf("kvlite: module artifact path: %w", err)
	}

	library, err := openModuleSharedLibrary(artifactPath)
	if err != nil {
		return nil, err
	}

	storage := &moduleDriverEngine{
		library: library,
		handle:  0,
	}
	if storage.handle, err = library.open(path, string(module.Manifest.Driver)); err != nil {
		library.unload()
		return nil, err
	}

	return storage, nil
}

func openModuleSharedLibrary(artifactPath string) (*moduleLibrary, error) {
	cPath := C.CString(artifactPath)
	defer C.free(unsafe.Pointer(cPath))

	state := C.kvlite_module_api{}
	var cError *C.char
	if status := C.kvlite_module_load(cPath, &state, &cError); status != 0 {
		message := readCStringAndFree(cError)
		if message == "" {
			message = "unable to load module shared library"
		}
		return nil, fmt.Errorf("%w: %s", ErrDriverNotLoaded, message)
	}

	library := &moduleLibrary{state: &state}

	var cAbiVersion C.uint
	var abiError *C.char
	if status := C.kvlite_module_abi_version(library.state, &cAbiVersion, &abiError); status != 0 {
		library.unload()
		return nil, fmt.Errorf("kvlite: module ABI check failed: %s", readCStringAndFree(abiError))
	}
	if cAbiVersion != C.uint(ModuleABIVersion) {
		library.unload()
		return nil, fmt.Errorf("%w: module ABI mismatch, expected %d got %d", ErrModuleIncompatible, ModuleABIVersion, cAbiVersion)
	}
	return library, nil
}

func (library *moduleLibrary) open(path, driver string) (uint64, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	cDriver := C.CString(driver)
	defer C.free(unsafe.Pointer(cDriver))

	var cHandle C.ulonglong
	var cError *C.char
	status := C.kvlite_module_open(library.state, cPath, cDriver, &cHandle, &cError)
	return uint64(cHandle), nativeModuleStatusError(status, readCStringAndFree(cError), "open")
}

func (library *moduleLibrary) close(handle uint64) error {
	cError := (*C.char)(nil)
	status := C.kvlite_module_close(library.state, C.ulonglong(handle), &cError)
	return nativeModuleStatusError(status, readCStringAndFree(cError), "close")
}

func (library *moduleLibrary) put(handle uint64, key, value []byte, ttlSeconds int64) error {
	var keyPointer unsafe.Pointer
	if len(key) > 0 {
		keyPointer = unsafe.Pointer(&key[0])
	}
	var valuePointer unsafe.Pointer
	if len(value) > 0 {
		valuePointer = unsafe.Pointer(&value[0])
	}
	cError := (*C.char)(nil)
	status := C.kvlite_module_put(
		library.state,
		C.ulonglong(handle),
		keyPointer,
		C.size_t(len(key)),
		valuePointer,
		C.size_t(len(value)),
		C.longlong(ttlSeconds),
		&cError,
	)
	return nativeModuleStatusError(status, readCStringAndFree(cError), "put")
}

func (library *moduleLibrary) get(handle uint64, key []byte) ([]byte, error) {
	var keyPointer unsafe.Pointer
	if len(key) > 0 {
		keyPointer = unsafe.Pointer(&key[0])
	}
	var value unsafe.Pointer
	var valueLength C.size_t
	cError := (*C.char)(nil)
	status := C.kvlite_module_get(
		library.state,
		C.ulonglong(handle),
		keyPointer,
		C.size_t(len(key)),
		&value,
		&valueLength,
		&cError,
	)
	if err := nativeModuleStatusError(status, readCStringAndFree(cError), "get"); err != nil {
		return nil, err
	}
	if value == nil {
		return []byte{}, nil
	}
	if valueLength == 0 {
		C.kvlite_module_free(library.state, value)
		return []byte{}, nil
	}
	result := C.GoBytes(value, C.int(valueLength))
	C.kvlite_module_free(library.state, value)
	return result, nil
}

func (library *moduleLibrary) delete(handle uint64, key []byte) error {
	var keyPointer unsafe.Pointer
	if len(key) > 0 {
		keyPointer = unsafe.Pointer(&key[0])
	}
	cError := (*C.char)(nil)
	status := C.kvlite_module_delete(library.state, C.ulonglong(handle), keyPointer, C.size_t(len(key)), &cError)
	return nativeModuleStatusError(status, readCStringAndFree(cError), "delete")
}

func (library *moduleLibrary) unload() {
	C.kvlite_module_unload(library.state)
}

type moduleDriverEngine struct {
	library *moduleLibrary
	handle  uint64
	closed  bool
}

func (engine *moduleDriverEngine) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	if key == nil || len(key) == 0 {
		return nil, false, fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if engine.closed {
		return nil, false, ErrClosed
	}
	data, err := engine.library.get(engine.handle, key)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (engine *moduleDriverEngine) Put(_ context.Context, key, value []byte) error {
	if engine.closed {
		return ErrClosed
	}
	return engine.library.put(engine.handle, key, value, 0)
}

func (engine *moduleDriverEngine) Delete(_ context.Context, key []byte) error {
	if engine.closed {
		return ErrClosed
	}
	return engine.library.delete(engine.handle, key)
}

func (engine *moduleDriverEngine) ScanPrefix(_ context.Context, prefix []byte, callback func(key, value []byte) error) error {
	_ = prefix
	_ = callback
	return fmt.Errorf("%w: native module backend does not expose scan", ErrDriverUnavailable)
}

func (engine *moduleDriverEngine) Close() error {
	if engine.closed {
		return nil
	}
	engine.closed = true
	if engine.library == nil {
		return nil
	}
	if err := engine.library.close(engine.handle); err != nil {
		engine.library.unload()
		return err
	}
	engine.library.unload()
	return nil
}

func readCStringAndFree(pointer *C.char) string {
	if pointer == nil {
		return ""
	}
	message := C.GoString(pointer)
	C.free(unsafe.Pointer(pointer))
	return message
}

func nativeModuleStatusError(status C.int, message string, operation string) error {
	if status == 0 {
		return nil
	}
	if message == "" {
		message = fmt.Sprintf("module %s failed with status %d", operation, int(status))
	}
	switch int(status) {
	case 1:
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	case 2:
		return fmt.Errorf("%w: %s", ErrInvalidArgument, message)
	default:
		return fmt.Errorf("%w: %s", ErrDriverUnavailable, message)
	}
}
