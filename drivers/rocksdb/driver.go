// Package rocksdb registers KVLite's optional RocksDB driver.
//
// Import it for its side effect and build with the rocksdb tag:
//
//	import _ "github.com/webong/kvlite/drivers/rocksdb"
package rocksdb

import "github.com/webong/kvlite"

// Name is the stable KVLite driver selection name.
const Name kvlite.DriverName = kvlite.DriverRocksDB

type driver struct{}

func init() {
	kvlite.MustRegisterDriver(driver{})
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
