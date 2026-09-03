// Package berkeleydb registers KVLite's optional Berkeley DB C API driver.
//
// The adapter is linked only when built with the berkeleydb tag and a licensed
// Berkeley DB C library. Import it for its side effect before selecting the
// driver:
//
//	import _ "github.com/webong/kvlite/extensions/berkeleydb"
package berkeleydb

import "github.com/webong/kvlite"

// Name is the stable KVLite driver selection name.
const Name kvlite.DriverName = kvlite.DriverBerkeleyDB

// DatabaseFilename is the Berkeley DB Btree file stored in a KVLite database
// directory. KVLite's own KVLITE-MANIFEST.json remains alongside it.
const DatabaseFilename = "KVLITE-BERKELEYDB.db"

type driver struct{}

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
	kvlite.MustRegisterDriver(driver{})
}

// Manifest describes the adapter, not a bundled Berkeley DB distribution.
// The consuming application or native module bundle must supply a Berkeley DB
// library under terms it is entitled to use.
func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          string(Name),
		Kind:          kvlite.ModuleKindDriver,
		Version:       "v0.1.0",
		ModuleABI:     kvlite.ModuleABIVersion,
		Driver:        Name,
		Capabilities:  []string{"embedded-storage", "native-cgo", "license-gated"},
		License:       "LicenseRef-Oracle-BerkeleyDB-separate-distribution",
	}
}

func (driver) Info() kvlite.DriverInfo {
	return kvlite.DriverInfo{
		Driver:         Name,
		Implementation: "berkeleydb-c",
		Format:         "bdb-btree-v1",
		Version:        "compatible-c-api",
	}
}

func (driver) Available() error {
	return nativeAvailable()
}

func (driver) Open(path string, options kvlite.DriverOptions) (kvlite.Engine, error) {
	if err := nativeAvailable(); err != nil {
		return nil, err
	}
	return openNative(path, options)
}
