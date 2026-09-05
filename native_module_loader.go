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

typedef struct {
	int (*open)(const char *, kvlite_handle_t *, char **);
	int (*close)(kvlite_handle_t, char **);
	int (*put)(kvlite_handle_t, const void *, size_t, const void *, size_t, char **);
	int (*get)(kvlite_handle_t, const void *, size_t, void **, size_t *, char **);
	int (*delete)(kvlite_handle_t, const void *, size_t, char **);
	int (*scan_open)(kvlite_handle_t, const void *, size_t, kvlite_handle_t *, char **);
	int (*scan_next)(kvlite_handle_t, void **, size_t *, void **, size_t *, char **);
	int (*scan_close)(kvlite_handle_t, char **);
	void (*free)(void *);
} kvlite_native_ops_v1;

typedef struct {
	const char *name;
	const char *version;
	const char *driver;
	const char *capabilities;
} kvlite_native_info_v1;

typedef struct kvlite_native_registration {
	char *name;
	char *version;
	char *driver;
	char *capabilities;
	kvlite_native_ops_v1 ops;
	struct kvlite_native_registration *next;
} kvlite_native_registration;

typedef struct {
	void *library;
	kvlite_native_ops_v1 ops;
	kvlite_native_registration *registrations;
	char *error;
} kvlite_native_state;

typedef struct {
	unsigned int host_abi;
	int (*register_driver)(const kvlite_native_info_v1 *, const kvlite_native_ops_v1 *, char **);
	void (*log)(const char *);
} kvlite_native_host_api_v1;

typedef int (*kvlite_module_init_fn)(const kvlite_native_host_api_v1 *, char **);

static void native_set_error(char **out, const char *message) {
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

static char *native_strdup(const char *value) {
	if (value == NULL) {
		return NULL;
	}
	size_t length = strlen(value);
	char *copy = (char *)malloc(length + 1);
	if (copy == NULL) {
		return NULL;
	}
	memcpy(copy, value, length + 1);
	return copy;
}

static const char *native_platform_error(void) {
#ifdef _WIN32
	return "unable to load KVLite native module";
#else
	const char *error = dlerror();
	return error == NULL ? "dynamic loader error" : error;
#endif
}

static void *native_load_library(const char *path) {
#ifdef _WIN32
	return (void *)LoadLibraryA(path);
#else
	return dlopen(path, RTLD_NOW | RTLD_LOCAL);
#endif
}

static void *native_resolve_symbol(void *library, const char *name) {
#ifdef _WIN32
	return (void *)GetProcAddress((HMODULE)library, name);
#else
	return dlsym(library, name);
#endif
}

// active_state receives register_driver calls while a module init function
// runs. Module loads are serialized by the Go side, so one slot suffices.
static kvlite_native_state *native_active_state = NULL;

static int native_register_driver(const kvlite_native_info_v1 *info, const kvlite_native_ops_v1 *ops, char **out_error) {
	kvlite_native_state *state = native_active_state;
	if (state == NULL) {
		native_set_error(out_error, "no native module load is active");
		return 1;
	}
	if (info == NULL || ops == NULL) {
		native_set_error(out_error, "module registered a null driver");
		return 1;
	}
	if (info->name == NULL || info->name[0] == '\0' || info->driver == NULL || info->driver[0] == '\0') {
		native_set_error(out_error, "module driver info needs a name and a driver");
		return 1;
	}
	if (ops->open == NULL || ops->close == NULL || ops->put == NULL ||
	    ops->get == NULL || ops->delete == NULL || ops->free == NULL) {
		native_set_error(out_error, "module driver is missing a required operation");
		return 1;
	}
	kvlite_native_registration *registration = (kvlite_native_registration *)calloc(1, sizeof(*registration));
	if (registration == NULL) {
		native_set_error(out_error, "out of memory");
		return 1;
	}
	registration->name = native_strdup(info->name);
	registration->version = native_strdup(info->version);
	registration->driver = native_strdup(info->driver);
	registration->capabilities = native_strdup(info->capabilities);
	registration->ops = *ops;
	registration->next = state->registrations;
	state->registrations = registration;
	return 0;
}

static int native_load_module(const char *path, kvlite_native_state *state, char **out_error) {
	if (path == NULL || path[0] == '\0') {
		native_set_error(out_error, "module path is required");
		return 1;
	}
	memset(state, 0, sizeof(*state));
	state->library = native_load_library(path);
	if (state->library == NULL) {
		native_set_error(out_error, native_platform_error());
		return 1;
	}
	kvlite_module_init_fn init = (kvlite_module_init_fn)native_resolve_symbol(state->library, "kvlite_module_init_v1");
	if (init == NULL) {
		native_set_error(out_error, "library does not export kvlite_module_init_v1");
#ifdef _WIN32
		FreeLibrary((HMODULE)state->library);
#else
		dlclose(state->library);
#endif
		state->library = NULL;
		return 1;
	}
	kvlite_native_host_api_v1 host;
	memset(&host, 0, sizeof(host));
	host.host_abi = 1;
	host.register_driver = native_register_driver;
	host.log = NULL;
	native_active_state = state;
	char *init_error = NULL;
	int status = init(&host, &init_error);
	native_active_state = NULL;
	if (status != 0) {
		native_set_error(out_error, init_error == NULL || init_error[0] == '\0' ? "module initialization failed" : init_error);
		free(init_error);
#ifdef _WIN32
		FreeLibrary((HMODULE)state->library);
#else
		dlclose(state->library);
#endif
		state->library = NULL;
		return 1;
	}
	if (state->registrations == NULL) {
		native_set_error(out_error, "module initialized without registering a driver");
#ifdef _WIN32
		FreeLibrary((HMODULE)state->library);
#else
		dlclose(state->library);
#endif
		state->library = NULL;
		return 1;
	}
	// The operation table stays valid because a successfully initialized
	// module is never unloaded. The host copies the selected registration's
	// table below and keeps the library loaded for the process lifetime.
	return 0;
}

static void native_free_registrations(kvlite_native_state *state) {
	kvlite_native_registration *registration = state->registrations;
	while (registration != NULL) {
		kvlite_native_registration *next = registration->next;
		free(registration->name);
		free(registration->version);
		free(registration->driver);
		free(registration->capabilities);
		free(registration);
		registration = next;
	}
	state->registrations = NULL;
}

// The wrappers below drive the selected registration's operation table.
// Scan members are optional: a module without them serves key operations and
// reports scans as unavailable.
static int native_ops_open(kvlite_native_state *state, const char *path, kvlite_handle_t *out_handle, char **out_error) {
	return state->ops.open(path, out_handle, out_error);
}

static int native_ops_close(kvlite_native_state *state, kvlite_handle_t handle, char **out_error) {
	return state->ops.close(handle, out_error);
}

static int native_ops_put(kvlite_native_state *state, kvlite_handle_t handle, const void *key, size_t key_length, const void *value, size_t value_length, char **out_error) {
	return state->ops.put(handle, key, key_length, value, value_length, out_error);
}

static int native_ops_get(kvlite_native_state *state, kvlite_handle_t handle, const void *key, size_t key_length, void **out_value, size_t *out_length, char **out_error) {
	return state->ops.get(handle, key, key_length, out_value, out_length, out_error);
}

static int native_ops_delete(kvlite_native_state *state, kvlite_handle_t handle, const void *key, size_t key_length, char **out_error) {
	return state->ops.delete(handle, key, key_length, out_error);
}

static int native_ops_scan_open(kvlite_native_state *state, kvlite_handle_t handle, const void *prefix, size_t prefix_length, kvlite_handle_t *out_cursor, char **out_error) {
	if (state->ops.scan_open == NULL) {
		native_set_error(out_error, "native module backend does not expose scan");
		return 3;
	}
	return state->ops.scan_open(handle, prefix, prefix_length, out_cursor, out_error);
}

static int native_ops_scan_next(kvlite_native_state *state, kvlite_handle_t cursor, void **out_key, size_t *out_key_length, void **out_value, size_t *out_value_length, char **out_error) {
	return state->ops.scan_next(cursor, out_key, out_key_length, out_value, out_value_length, out_error);
}

static int native_ops_scan_close(kvlite_native_state *state, kvlite_handle_t cursor, char **out_error) {
	if (state->ops.scan_close == NULL) {
		return 0;
	}
	return state->ops.scan_close(cursor, out_error);
}

static void native_ops_free(kvlite_native_state *state, void *pointer) {
	if (state->ops.free != NULL && pointer != NULL) {
		state->ops.free(pointer);
	}
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"
)

// nativeModuleLibrary is one loaded native-module shared library. The library
// stays loaded for the process lifetime: operation tables must remain valid
// and already-opened engines keep calling into them.
type nativeModuleLibrary struct {
	state *C.kvlite_native_state
}

// nativeModuleRegistration is one driver registration collected while a
// module init function ran.
type nativeModuleRegistration struct {
	name         string
	version      string
	driver       DriverName
	capabilities []string
}

// nativeModuleDriver adapts one native-module driver registration to the
// in-process Driver interface so Open resolves it like a linked driver.
type nativeModuleDriver struct {
	library      *nativeModuleLibrary
	info         DriverInfo
	registration nativeModuleRegistration
}

var nativeModuleLoads = struct {
	sync.Mutex
	drivers map[string]registeredDriver
}{drivers: make(map[string]registeredDriver)}

func openNativeModuleDriver(path string, module Module, options DriverOptions) (Engine, error) {
	_ = options
	nativeModuleLoads.Lock()
	defer nativeModuleLoads.Unlock()
	if registered, found := nativeModuleLoads.drivers[module.Directory]; found {
		return registered.driver.Open(path, options)
	}
	wanted, err := normalizeDriverName(module.Manifest.Driver)
	if err != nil {
		return nil, err
	}
	library, selected, err := loadNativeModuleLibrary(module, wanted)
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(module.Manifest.Version)
	if version == "" {
		version = "unknown"
	}
	driver := &nativeModuleDriver{
		library: library,
		info: DriverInfo{
			Driver:         wanted,
			Backend:        Backend(wanted),
			Implementation: module.Manifest.Name,
			Format:         version,
			Version:        version,
			Available:      true,
		},
		registration: selected,
	}
	if err := RegisterDriver(driver); err != nil {
		return nil, fmt.Errorf("kvlite: register native driver %q: %w", wanted, err)
	}
	registered := registeredDriver{driver: driver, info: driver.info}
	nativeModuleLoads.drivers[module.Directory] = registered
	return driver.Open(path, options)
}

// loadNativeModuleLibrary loads one native-module shared library, runs its
// kvlite_module_init_v1 entry point, and selects the registration matching
// the manifest's driver. The library stays loaded for the process lifetime.
func loadNativeModuleLibrary(module Module, wanted DriverName) (*nativeModuleLibrary, nativeModuleRegistration, error) {
	var selected nativeModuleRegistration
	artifact, err := module.ArtifactForCurrentPlatform(ModuleArtifactNative)
	if err != nil {
		return nil, selected, fmt.Errorf("kvlite: open native module artifact: %w", err)
	}
	if artifact.Symbol != "kvlite_module_init_v1" {
		return nil, selected, fmt.Errorf("%w: native module %q names initialization symbol %q, want %q", ErrModuleIncompatible, module.Manifest.Name, artifact.Symbol, "kvlite_module_init_v1")
	}
	artifactPath, err := module.ArtifactPath(artifact)
	if err != nil {
		return nil, selected, fmt.Errorf("kvlite: native module artifact path: %w", err)
	}
	cPath := C.CString(artifactPath)
	defer C.free(unsafe.Pointer(cPath))
	var state C.kvlite_native_state
	var cError *C.char
	if status := C.native_load_module(cPath, &state, &cError); status != 0 {
		message := readCStringAndFree(cError)
		if message == "" {
			message = "unable to load native module"
		}
		return nil, selected, fmt.Errorf("%w: %s", ErrDriverNotLoaded, message)
	}
	registrations, opsTables := readNativeRegistrations(&state)
	C.native_free_registrations(&state)
	matched := -1
	for index := range registrations {
		if registrations[index].driver == wanted {
			matched = index
			break
		}
	}
	if matched < 0 {
		return nil, selected, fmt.Errorf("%w: native module %q did not register driver %q", ErrModuleIncompatible, module.Manifest.Name, wanted)
	}
	selected = registrations[matched]
	// Copy the selected operation table into heap state kept with the
	// library. The module itself stays loaded, so the function pointers
	// remain valid for the process lifetime.
	library := &nativeModuleLibrary{state: &C.kvlite_native_state{}}
	library.state.library = state.library
	library.state.ops = opsTables[matched]
	library.state.registrations = nil
	library.state.error = nil
	return library, selected, nil
}

// readNativeRegistrations walks the C registration list once, copying every
// string into Go memory. Callers must free the C list afterwards.
func readNativeRegistrations(state *C.kvlite_native_state) ([]nativeModuleRegistration, []C.kvlite_native_ops_v1) {
	registrations := make([]nativeModuleRegistration, 0)
	opsTables := make([]C.kvlite_native_ops_v1, 0)
	for registration := state.registrations; registration != nil; registration = registration.next {
		name := strings.ToLower(strings.TrimSpace(C.GoString(registration.name)))
		version := strings.TrimSpace(C.GoString(registration.version))
		driver, err := normalizeDriverName(DriverName(C.GoString(registration.driver)))
		if err != nil || name == "" {
			continue
		}
		capabilities := make([]string, 0)
		for _, capability := range strings.Split(C.GoString(registration.capabilities), ",") {
			capability = strings.ToLower(strings.TrimSpace(capability))
			if capability != "" {
				capabilities = append(capabilities, capability)
			}
		}
		registrations = append(registrations, nativeModuleRegistration{
			name:         name,
			version:      version,
			driver:       driver,
			capabilities: capabilities,
		})
		opsTables = append(opsTables, registration.ops)
	}
	return registrations, opsTables
}

func (driver *nativeModuleDriver) Info() DriverInfo { return driver.info }

func (driver *nativeModuleDriver) Available() error { return nil }

func (driver *nativeModuleDriver) Open(path string, options DriverOptions) (Engine, error) {
	_ = options
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var cHandle C.ulonglong
	var cError *C.char
	if status := C.native_ops_open(driver.library.state, cPath, &cHandle, &cError); status != 0 {
		return nil, nativeModuleStatusError(status, readCStringAndFree(cError), "open")
	}
	return &nativeModuleEngine{library: driver.library, handle: uint64(cHandle)}, nil
}

type nativeModuleEngine struct {
	library *nativeModuleLibrary
	handle  uint64
	closed  bool
}

func (engine *nativeModuleEngine) Get(_ context.Context, key []byte) ([]byte, bool, error) {
	if key == nil || len(key) == 0 {
		return nil, false, fmt.Errorf("%w: key is required", ErrInvalidArgument)
	}
	if engine.closed {
		return nil, false, ErrClosed
	}
	var keyPointer unsafe.Pointer
	if len(key) > 0 {
		keyPointer = unsafe.Pointer(&key[0])
	}
	var value unsafe.Pointer
	var valueLength C.size_t
	cError := (*C.char)(nil)
	status := C.native_ops_get(engine.library.state, C.ulonglong(engine.handle), keyPointer, C.size_t(len(key)), &value, &valueLength, &cError)
	if err := nativeModuleStatusError(status, readCStringAndFree(cError), "get"); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if value == nil || valueLength == 0 {
		C.native_ops_free(engine.library.state, value)
		return []byte{}, true, nil
	}
	result := C.GoBytes(value, C.int(valueLength))
	C.native_ops_free(engine.library.state, value)
	return result, true, nil
}

func (engine *nativeModuleEngine) Put(_ context.Context, key, value []byte) error {
	if engine.closed {
		return ErrClosed
	}
	var keyPointer unsafe.Pointer
	if len(key) > 0 {
		keyPointer = unsafe.Pointer(&key[0])
	}
	var valuePointer unsafe.Pointer
	if len(value) > 0 {
		valuePointer = unsafe.Pointer(&value[0])
	}
	cError := (*C.char)(nil)
	status := C.native_ops_put(engine.library.state, C.ulonglong(engine.handle), keyPointer, C.size_t(len(key)), valuePointer, C.size_t(len(value)), &cError)
	return nativeModuleStatusError(status, readCStringAndFree(cError), "put")
}

func (engine *nativeModuleEngine) Delete(_ context.Context, key []byte) error {
	if engine.closed {
		return ErrClosed
	}
	var keyPointer unsafe.Pointer
	if len(key) > 0 {
		keyPointer = unsafe.Pointer(&key[0])
	}
	cError := (*C.char)(nil)
	status := C.native_ops_delete(engine.library.state, C.ulonglong(engine.handle), keyPointer, C.size_t(len(key)), &cError)
	return nativeModuleStatusError(status, readCStringAndFree(cError), "delete")
}

func (engine *nativeModuleEngine) ScanPrefix(_ context.Context, prefix []byte, callback func(key, value []byte) error) error {
	if engine.closed {
		return ErrClosed
	}
	var prefixPointer unsafe.Pointer
	if len(prefix) > 0 {
		prefixPointer = unsafe.Pointer(&prefix[0])
	}
	var cCursor C.ulonglong
	cError := (*C.char)(nil)
	if status := C.native_ops_scan_open(engine.library.state, C.ulonglong(engine.handle), prefixPointer, C.size_t(len(prefix)), &cCursor, &cError); status != 0 {
		return nativeModuleStatusError(status, readCStringAndFree(cError), "scan open")
	}
	cursor := uint64(cCursor)
	closeCursor := func() error {
		cCloseError := (*C.char)(nil)
		status := C.native_ops_scan_close(engine.library.state, C.ulonglong(cursor), &cCloseError)
		return nativeModuleStatusError(status, readCStringAndFree(cCloseError), "scan close")
	}
	for {
		var cKey, cValue unsafe.Pointer
		var cKeyLength, cValueLength C.size_t
		cNextError := (*C.char)(nil)
		status := C.native_ops_scan_next(engine.library.state, C.ulonglong(cursor), &cKey, &cKeyLength, &cValue, &cValueLength, &cNextError)
		if status == 1 {
			break
		}
		if err := nativeModuleStatusError(status, readCStringAndFree(cNextError), "scan next"); err != nil {
			_ = closeCursor()
			return err
		}
		key := C.GoBytes(cKey, C.int(cKeyLength))
		C.native_ops_free(engine.library.state, cKey)
		value := C.GoBytes(cValue, C.int(cValueLength))
		C.native_ops_free(engine.library.state, cValue)
		if err := callback(key, value); err != nil {
			_ = closeCursor()
			return err
		}
	}
	return closeCursor()
}

func (engine *nativeModuleEngine) Close() error {
	if engine.closed {
		return nil
	}
	engine.closed = true
	cError := (*C.char)(nil)
	status := C.native_ops_close(engine.library.state, C.ulonglong(engine.handle), &cError)
	return nativeModuleStatusError(status, readCStringAndFree(cError), "close")
}
