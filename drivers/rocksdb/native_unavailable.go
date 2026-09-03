//go:build !rocksdb

package rocksdb

import "github.com/webong/kvlite"

func nativeAvailable() error {
	return kvlite.ErrRocksDBNotBuilt
}

func openNative(_ string, _ kvlite.DriverOptions) (kvlite.Engine, error) {
	return nil, kvlite.ErrRocksDBNotBuilt
}
