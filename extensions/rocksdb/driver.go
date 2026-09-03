// Package rocksdb registers KVLite's optional RocksDB driver extension.
//
// Import it for its side effect and build with the rocksdb tag:
//
//	import _ "github.com/webong/kvlite/extensions/rocksdb"
package rocksdb

import "github.com/webong/kvlite"

// Name is the stable KVLite driver selection name.
const Name kvlite.DriverName = kvlite.DriverRocksDB

type driver struct{}

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
	kvlite.MustRegisterDriver(driver{})
}

// Manifest describes this linked Go driver using the same metadata shape as a
// standalone KVLite module bundle. Release artifacts supply checksummed native
// entries through kvlite-module.json; this source module remains the explicit
// Go-import development path.
func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          string(Name),
		Kind:          kvlite.ModuleKindDriver,
		Version:       "v0.1.0",
		ModuleABI:     kvlite.ModuleABIVersion,
		Driver:        Name,
		Capabilities:  []string{"embedded-storage", "ttl-compaction"},
		License:       "Apache-2.0",
	}
}

func (driver) Info() kvlite.DriverInfo {
	return kvlite.DriverInfo{
		Driver:              Name,
		Implementation:      "grocksdb",
		Format:              "v1",
		Version:             "v1.10.6",
		PhysicalTTLCompacts: true,
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
