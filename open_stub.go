//go:build !rocksdb

package kvlite

func openRocksEngine(_ string, _ config) (engine, error) {
	return nil, ErrRocksDBNotBuilt
}
