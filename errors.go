package kvlite

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound         = errors.New("kvlite: key not found")
	ErrClosed           = errors.New("kvlite: database is closed")
	ErrInvalidArgument  = errors.New("kvlite: invalid argument")
	ErrCodecUnavailable = errors.New("kvlite: value codec is unavailable")
	// ErrDriverUnavailable means an installed driver cannot run in this build
	// or server environment (for example, a RocksDB driver built without its
	// native tag).
	ErrDriverUnavailable = errors.New("kvlite: storage driver is unavailable")
	// ErrRocksDBNotBuilt is a specific unavailable-driver error that callers
	// can still distinguish with errors.Is.
	ErrRocksDBNotBuilt = fmt.Errorf("%w: RocksDB support is not built; rebuild with -tags rocksdb", ErrDriverUnavailable)
	// ErrBackendUnavailable preserves the original backend spelling.
	ErrBackendUnavailable = ErrDriverUnavailable
	// ErrDriverNotInstalled lets applications distinguish an unsupported name
	// from a known target whose optional driver was not packaged in this build.
	ErrDriverNotInstalled = errors.New("kvlite: storage driver is not installed")
	// ErrModuleNotInstalled means no linked or discovered KVLite module has the
	// requested stable module name. It is intentionally separate from
	// ErrDriverNotInstalled because HTTP, Redis, codecs, and other optional
	// capabilities are modules too.
	ErrModuleNotInstalled = errors.New("kvlite: module is not installed")
	// ErrModuleManifestInvalid means a discovered module descriptor is malformed
	// or unsafe to load. KVLite never guesses from an arbitrary library name.
	ErrModuleManifestInvalid = errors.New("kvlite: module manifest is invalid")
	// ErrModuleIncompatible means a module was built for a different KVLite
	// module manifest ABI.
	ErrModuleIncompatible = errors.New("kvlite: module is incompatible")
	// ErrModuleConflict prevents two installed artifacts from silently claiming
	// the same stable KVLite module name.
	ErrModuleConflict = errors.New("kvlite: module name conflict")
	// ErrModuleArtifactMissing means a manifest refers to an artifact which is
	// absent for this installed module.
	ErrModuleArtifactMissing = errors.New("kvlite: module artifact is missing")
	// ErrModuleIntegrity means an artifact does not match its manifest checksum.
	ErrModuleIntegrity = errors.New("kvlite: module artifact integrity check failed")
	// ErrDriverNotExposed means a remote server has the driver installed but
	// has not mapped any server-owned database path for it.
	ErrDriverNotExposed = errors.New("kvlite: storage driver is not exposed by this server")
	// ErrBackendMismatch prevents a directory initialized for one storage
	// backend from being opened through another backend.
	ErrBackendMismatch = errors.New("kvlite: database backend mismatch")
	// ErrBackendManifestMissing means a non-empty directory predates KVLite's
	// backend manifest and cannot be safely adopted by an explicitly selected
	// backend.
	ErrBackendManifestMissing = errors.New("kvlite: database backend manifest is missing")
	// ErrBackendManifestInvalid means the path contains malformed or unsupported
	// KVLite backend metadata.
	ErrBackendManifestInvalid = errors.New("kvlite: database backend manifest is invalid")
	// ErrBerkeleyDBNotBuilt is deliberately separate from a generic backend
	// error so callers can explain that Berkeley DB needs an explicit,
	// license-reviewed distribution choice.
	ErrBerkeleyDBNotBuilt = fmt.Errorf("%w: Berkeley DB support is not built; rebuild with -tags berkeleydb and link a licensed Berkeley DB distribution", ErrDriverUnavailable)
)
