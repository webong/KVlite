package kvlite

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// DriverName identifies the installed storage driver selected for one KVLite
// database. It is deliberately independent of an engine's implementation
// language or on-disk format.
type DriverName string

// Backend remains a compatibility alias for DriverName. New APIs use the word
// "driver" because KVLite's core is engine-neutral.
type Backend = DriverName

const (
	// DriverRocksDB is the stable name provided by extensions/rocksdb.
	DriverRocksDB DriverName = "rocksdb"
	// DriverLevelDB is the stable name provided by extensions/leveldb.
	DriverLevelDB DriverName = "leveldb"
	// DriverBerkeleyDB is reserved for a separately distributed Berkeley DB
	// driver. The core intentionally does not register or link it.
	DriverBerkeleyDB DriverName = "berkeleydb"

	// BackendRocksDB, BackendLevelDB, and BackendBerkeleyDB preserve the first
	// public API's spelling. Prefer the Driver* constants in new code.
	BackendRocksDB    = DriverRocksDB
	BackendLevelDB    = DriverLevelDB
	BackendBerkeleyDB = DriverBerkeleyDB
	// BackendRemote is reported by a remote DB when its client did not select a
	// driver explicitly and therefore follows the server default.
	BackendRemote DriverName = "remote"
)

// DriverOptions contains the resource profile and record hooks supplied by
// KVLite core to a driver. Drivers must not depend on KVLite's private config
// type, which keeps them independently buildable Go modules.
type DriverOptions struct {
	BlockCacheSize    int
	WriteBufferSize   int
	MaxWriteBuffers   int
	MaxBackgroundJobs int
	// RecordExpired reports whether a raw KVLite record is expired now. Drivers
	// with physical compaction can use it without knowing the envelope format.
	RecordExpired func([]byte) bool
}

// DriverInfo describes an installed driver. Implementation and Format are
// persisted in each database manifest; Version is descriptive and can change
// without making a compatible database unreadable.
type DriverInfo struct {
	// Driver is the stable name used by WithDriver and the remote protocol.
	Driver DriverName `json:"driver"`
	// Backend is a compatibility mirror of Driver for pre-driver clients.
	Backend Backend `json:"backend"`

	Implementation      string `json:"implementation"`
	Format              string `json:"format"`
	Version             string `json:"version"`
	Available           bool   `json:"available"`
	PhysicalTTLCompacts bool   `json:"physical_ttl_compacts"`
}

// BackendInfo preserves the former discovery API. It is identical to
// DriverInfo and will remain so through KVLite v1.
type BackendInfo = DriverInfo

// Driver is the in-process Go adapter implemented by a linked storage module.
// Independently distributed module artifacts are described by
// kvlite-module.json and resolved through the module catalog; a host can then
// choose an appropriate loading strategy. For the normal Go development path,
// a linked module registers its Driver from init:
//
//	import _ "github.com/webong/kvlite/extensions/rocksdb"
//
// A driver is not selected merely because it is installed; callers still use
// WithDriver when they open a database.
type Driver interface {
	Info() DriverInfo
	Available() error
	Open(path string, options DriverOptions) (Engine, error)
}

type registeredDriver struct {
	driver Driver
	info   DriverInfo
}

var driverRegistry = struct {
	sync.RWMutex
	drivers map[DriverName]registeredDriver
}{drivers: make(map[DriverName]registeredDriver)}

// RegisterDriver registers an engine driver with the process-local KVLite
// registry. It is intended for driver modules, normally from init. Duplicate
// registrations are rejected so an application cannot silently change which
// implementation owns an existing KVLite directory.
func RegisterDriver(driver Driver) error {
	if driver == nil {
		return fmt.Errorf("%w: storage driver is required", ErrInvalidArgument)
	}
	info := driver.Info()
	name := info.Driver
	if name == "" {
		name = DriverName(info.Backend)
	}
	canonical, err := normalizeDriverName(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(info.Implementation) == "" || strings.TrimSpace(info.Format) == "" || strings.TrimSpace(info.Version) == "" {
		return fmt.Errorf("%w: driver %q must provide implementation, format, and version metadata", ErrInvalidArgument, canonical)
	}
	info.Driver = canonical
	info.Backend = Backend(canonical)

	driverRegistry.Lock()
	defer driverRegistry.Unlock()
	if _, exists := driverRegistry.drivers[canonical]; exists {
		return fmt.Errorf("%w: storage driver %q is already registered", ErrInvalidArgument, canonical)
	}
	driverRegistry.drivers[canonical] = registeredDriver{driver: driver, info: info}
	return nil
}

// MustRegisterDriver is RegisterDriver for a driver's init function.
func MustRegisterDriver(driver Driver) {
	if err := RegisterDriver(driver); err != nil {
		panic(err)
	}
}

// Drivers returns drivers linked into this process. Use DiscoverModules to
// inspect separately installed artifact manifests. Drivers that were imported
// but cannot run in the current native build are still listed with Available
// false and an actionable Open error.
func Drivers() []DriverInfo {
	driverRegistry.RLock()
	names := make([]DriverName, 0, len(driverRegistry.drivers))
	for name := range driverRegistry.drivers {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool { return names[left] < names[right] })
	registered := make([]registeredDriver, len(names))
	for index, name := range names {
		registered[index] = driverRegistry.drivers[name]
	}
	driverRegistry.RUnlock()

	result := make([]DriverInfo, 0, len(registered))
	for _, item := range registered {
		info := item.info
		info.Available = item.driver.Available() == nil
		result = append(result, info)
	}
	return result
}

// DriverInfoFor returns metadata for one driver linked into this process.
func DriverInfoFor(name DriverName) (DriverInfo, error) {
	_, registered, err := registeredDriverFor(name)
	if err != nil {
		return DriverInfo{}, err
	}
	info := registered.info
	info.Available = registered.driver.Available() == nil
	return info, nil
}

// Backends and BackendInfoFor are retained source-compatible aliases for the
// original backend vocabulary.
func Backends() []BackendInfo { return Drivers() }

func BackendInfoFor(backend Backend) (BackendInfo, error) {
	return DriverInfoFor(DriverName(backend))
}

// DefaultDriver returns the driver a bundled surface should use when its
// caller did not name one. It preserves RocksDB as the compatibility default
// when RocksDB is registered; otherwise it selects the only installed driver.
// If more than one non-RocksDB driver is installed, it returns RocksDB so the
// caller receives the normal explicit-driver error instead of guessing.
//
// Open itself intentionally continues to request RocksDB by default. This
// helper is for a driver-specific CLI or C shared-library bundle, where a
// LevelDB-only build should make its one bundled driver useful without a
// separate selector argument.
func DefaultDriver() DriverName {
	if _, _, err := registeredDriverFor(DriverRocksDB); err == nil {
		return DriverRocksDB
	}
	drivers := Drivers()
	if len(drivers) == 1 {
		return drivers[0].Driver
	}
	return DriverRocksDB
}

func (driver DriverName) String() string { return string(driver) }

// ParseDriverName validates and canonicalizes a storage driver name. It is
// useful to optional extensions that accept a driver name without selecting a
// local directory themselves.
func ParseDriverName(name string) (DriverName, error) {
	return normalizeDriverName(DriverName(name))
}

func normalizeDriverName(name DriverName) (DriverName, error) {
	canonical := DriverName(strings.ToLower(strings.TrimSpace(string(name))))
	if canonical == "" {
		return "", fmt.Errorf("%w: storage driver is required", ErrInvalidArgument)
	}
	for index, character := range canonical {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return "", fmt.Errorf("%w: storage driver %q contains an unsupported character at byte %d", ErrInvalidArgument, name, index)
	}
	return canonical, nil
}

func registeredDriverFor(name DriverName) (DriverName, registeredDriver, error) {
	canonical, err := normalizeDriverName(name)
	if err != nil {
		return "", registeredDriver{}, err
	}
	driverRegistry.RLock()
	registered, found := driverRegistry.drivers[canonical]
	driverRegistry.RUnlock()
	if !found {
		return "", registeredDriver{}, fmt.Errorf("%w: %q", ErrDriverNotInstalled, canonical)
	}
	return canonical, registered, nil
}

func (cfg config) driverOptions() DriverOptions {
	return DriverOptions{
		BlockCacheSize:    cfg.blockCacheSize,
		WriteBufferSize:   cfg.writeBufferSize,
		MaxWriteBuffers:   cfg.maxWriteBuffers,
		MaxBackgroundJobs: cfg.maxBackgroundJobs,
		RecordExpired: func(record []byte) bool {
			return envelopeExpired(record, cfg.now())
		},
	}
}

func resolveLinkedDriverOrReportModule(name DriverName, driverExplicit bool) (DriverName, registeredDriver, error) {
	registeredName, registered, err := registeredDriverFor(name)
	if err == nil {
		return registeredName, registered, nil
	}
	if !errors.Is(err, ErrDriverNotInstalled) {
		return "", registeredDriver{}, err
	}
	if !driverExplicit {
		return "", registeredDriver{}, err
	}

	module, moduleErr := ResolveModule(name)
	if moduleErr != nil {
		return "", registeredDriver{}, err
	}
	if module.Manifest.Kind != ModuleKindDriver {
		return "", registeredDriver{}, fmt.Errorf("%w: installed module %q is not a driver module", ErrDriverNotLoaded, name)
	}
	if _, _, err := module.ArtifactForCurrentPlatform(ModuleArtifactCShared, ModuleArtifactExecutable); err != nil {
		return "", registeredDriver{}, fmt.Errorf("%w: installed driver %q is available, but this binary did not load its adapter: %v", ErrDriverNotLoaded, name, err)
	}
	return "", registeredDriver{}, fmt.Errorf("%w: installed driver %q has a runtime module at %s that is not linked into this process", ErrDriverNotLoaded, name, module.Manifest.Name)
}

func openConfiguredEngine(path string, cfg config) (Engine, Backend, error) {
	name, registered, err := resolveLinkedDriverOrReportModule(cfg.driver, cfg.driverExplicit)
	if err != nil {
		return nil, "", err
	}
	if err := registered.driver.Available(); err != nil {
		return nil, "", fmt.Errorf("kvlite: open driver %q: %w", name, err)
	}

	legacyManifest, err := prepareBackendManifest(path, registered, !cfg.driverExplicit && name == DriverRocksDB)
	if err != nil {
		return nil, "", err
	}
	storage, err := registered.driver.Open(path, cfg.driverOptions())
	if err != nil {
		return nil, "", err
	}
	if legacyManifest {
		if err := createBackendManifest(path, registered); err != nil {
			_ = storage.Close()
			return nil, "", err
		}
	}
	return storage, Backend(name), nil
}
