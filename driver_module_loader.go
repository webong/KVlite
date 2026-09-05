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
typedef void (*kvlite_free_fn)(void *);
// Raw record-store operations address the engine keyspace directly. They are
// additive ABI v1 symbols; a bundle that predates them cannot serve a module
// driver engine and is rejected with a clear missing-symbol error instead of
// routing engine operations through the logical functions.
typedef int (*kvlite_raw_put_fn)(kvlite_handle_t, const void *, size_t, const void *, size_t, char **);
typedef int (*kvlite_raw_get_fn)(kvlite_handle_t, const void *, size_t, void **, size_t *, char **);
typedef int (*kvlite_raw_delete_fn)(kvlite_handle_t, const void *, size_t, char **);
typedef int (*kvlite_raw_scan_open_fn)(kvlite_handle_t, const void *, size_t, kvlite_handle_t *, char **);
typedef int (*kvlite_raw_scan_next_fn)(kvlite_handle_t, void **, size_t *, void **, size_t *, char **);
typedef int (*kvlite_raw_scan_close_fn)(kvlite_handle_t, char **);

typedef struct {
	void *library;
	kvlite_abi_version_fn abi_version;
	kvlite_open_with_driver_fn open_with_driver;
	kvlite_open_with_backend_fn open_with_backend;
	kvlite_open_fn open;
	kvlite_close_fn close;
	kvlite_free_fn free_ptr;
	kvlite_raw_put_fn raw_put;
	kvlite_raw_get_fn raw_get;
	kvlite_raw_delete_fn raw_delete;
	kvlite_raw_scan_open_fn raw_scan_open;
	kvlite_raw_scan_next_fn raw_scan_next;
	kvlite_raw_scan_close_fn raw_scan_close;
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
	// Intentionally never unloads: a Go-built shared library starts its own
	// runtime on load, and that runtime cannot be torn down safely. Unmapping
	// it while its threads still exist corrupts the host process (observed as
	// unkillable CPU spin). The mapping is process-lifetime by design; the
	// host keeps at most one per distinct driver bundle. Callers clear their
	// handle below.
	(void)library;
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
	RESOLVE(raw_put, "kvlite_raw_put", kvlite_raw_put_fn);
	RESOLVE(raw_get, "kvlite_raw_get", kvlite_raw_get_fn);
	RESOLVE(raw_delete, "kvlite_raw_delete", kvlite_raw_delete_fn);
	RESOLVE(raw_scan_open, "kvlite_raw_scan_open", kvlite_raw_scan_open_fn);
	RESOLVE(raw_scan_next, "kvlite_raw_scan_next", kvlite_raw_scan_next_fn);
	RESOLVE(raw_scan_close, "kvlite_raw_scan_close", kvlite_raw_scan_close_fn);
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

static int kvlite_module_raw_put(const kvlite_module_api *api, kvlite_handle_t handle, const void *key, size_t key_length, const void *value, size_t value_length, char **out_error) {
	if (api == NULL || api->raw_put == NULL) {
		return 1;
	}
	return api->raw_put(handle, key, key_length, value, value_length, out_error);
}

static int kvlite_module_raw_get(const kvlite_module_api *api, kvlite_handle_t handle, const void *key, size_t key_length, void **out_value, size_t *out_length, char **out_error) {
	if (api == NULL || api->raw_get == NULL) {
		return 1;
	}
	return api->raw_get(handle, key, key_length, out_value, out_length, out_error);
}

static int kvlite_module_raw_delete(const kvlite_module_api *api, kvlite_handle_t handle, const void *key, size_t key_length, char **out_error) {
	if (api == NULL || api->raw_delete == NULL) {
		return 1;
	}
	return api->raw_delete(handle, key, key_length, out_error);
}

static int kvlite_module_raw_scan_open(const kvlite_module_api *api, kvlite_handle_t handle, const void *prefix, size_t prefix_length, kvlite_handle_t *out_cursor, char **out_error) {
	if (api == NULL || api->raw_scan_open == NULL) {
		return 1;
	}
	return api->raw_scan_open(handle, prefix, prefix_length, out_cursor, out_error);
}

static int kvlite_module_raw_scan_next(const kvlite_module_api *api, kvlite_handle_t cursor, void **out_key, size_t *out_key_length, void **out_value, size_t *out_value_length, char **out_error) {
	if (api == NULL || api->raw_scan_next == NULL) {
		return 1;
	}
	return api->raw_scan_next(cursor, out_key, out_key_length, out_value, out_value_length, out_error);
}

static int kvlite_module_raw_scan_close(const kvlite_module_api *api, kvlite_handle_t cursor, char **out_error) {
	if (api == NULL || api->raw_scan_close == NULL) {
		return 1;
	}
	return api->raw_scan_close(cursor, out_error);
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

func (library *moduleLibrary) put(handle uint64, key, value []byte) error {
	var keyPointer unsafe.Pointer
	if len(key) > 0 {
		keyPointer = unsafe.Pointer(&key[0])
	}
	var valuePointer unsafe.Pointer
	if len(value) > 0 {
		valuePointer = unsafe.Pointer(&value[0])
	}
	cError := (*C.char)(nil)
	status := C.kvlite_module_raw_put(
		library.state,
		C.ulonglong(handle),
		keyPointer,
		C.size_t(len(key)),
		valuePointer,
		C.size_t(len(value)),
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
	status := C.kvlite_module_raw_get(
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
	status := C.kvlite_module_raw_delete(library.state, C.ulonglong(handle), keyPointer, C.size_t(len(key)), &cError)
	return nativeModuleStatusError(status, readCStringAndFree(cError), "delete")
}

// scanPrefix streams one engine-keyspace prefix scan through a snapshot
// cursor. The cursor is always closed before returning.
func (library *moduleLibrary) scanPrefix(handle uint64, prefix []byte) ([][]byte, [][]byte, error) {
	var prefixPointer unsafe.Pointer
	if len(prefix) > 0 {
		prefixPointer = unsafe.Pointer(&prefix[0])
	}
	var cCursor C.ulonglong
	cError := (*C.char)(nil)
	if status := C.kvlite_module_raw_scan_open(library.state, C.ulonglong(handle), prefixPointer, C.size_t(len(prefix)), &cCursor, &cError); status != 0 {
		return nil, nil, nativeModuleStatusError(status, readCStringAndFree(cError), "scan open")
	}
	cursor := uint64(cCursor)
	closeErr := func() error {
		cCloseError := (*C.char)(nil)
		status := C.kvlite_module_raw_scan_close(library.state, C.ulonglong(cursor), &cCloseError)
		return nativeModuleStatusError(status, readCStringAndFree(cCloseError), "scan close")
	}
	var keys, values [][]byte
	for {
		var cKey, cValue unsafe.Pointer
		var cKeyLength, cValueLength C.size_t
		cNextError := (*C.char)(nil)
		status := C.kvlite_module_raw_scan_next(library.state, C.ulonglong(cursor), &cKey, &cKeyLength, &cValue, &cValueLength, &cNextError)
		if status == 1 {
			// Exhausted: KVLITE_NOT_FOUND with no error text ends the scan.
			break
		}
		if err := nativeModuleStatusError(status, readCStringAndFree(cNextError), "scan next"); err != nil {
			_ = closeErr()
			return nil, nil, err
		}
		key := C.GoBytes(cKey, C.int(cKeyLength))
		C.kvlite_module_free(library.state, cKey)
		value := C.GoBytes(cValue, C.int(cValueLength))
		C.kvlite_module_free(library.state, cValue)
		keys = append(keys, key)
		values = append(values, value)
	}
	if err := closeErr(); err != nil {
		return nil, nil, err
	}
	return keys, values, nil
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
	return engine.library.put(engine.handle, key, value)
}

func (engine *moduleDriverEngine) Delete(_ context.Context, key []byte) error {
	if engine.closed {
		return ErrClosed
	}
	return engine.library.delete(engine.handle, key)
}

func (engine *moduleDriverEngine) ScanPrefix(_ context.Context, prefix []byte, callback func(key, value []byte) error) error {
	if engine.closed {
		return ErrClosed
	}
	keys, values, err := engine.library.scanPrefix(engine.handle, prefix)
	if err != nil {
		return err
	}
	for index := range keys {
		if err := callback(keys[index], values[index]); err != nil {
			return err
		}
	}
	return nil
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
