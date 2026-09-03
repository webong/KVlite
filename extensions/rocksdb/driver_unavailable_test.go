//go:build !rocksdb

package rocksdb_test

import (
	"errors"
	"testing"

	"github.com/webong/kvlite"
	_ "github.com/webong/kvlite/extensions/rocksdb"
)

func TestRocksDBDriverReportsMissingNativeBuild(t *testing.T) {
	info, err := kvlite.DriverInfoFor(kvlite.DriverRocksDB)
	if err != nil {
		t.Fatal(err)
	}
	if info.Available {
		t.Fatalf("RocksDB driver unexpectedly reports available: %#v", info)
	}
	_, err = kvlite.Open(t.TempDir(), kvlite.WithDriver("rocksdb"))
	if !errors.Is(err, kvlite.ErrRocksDBNotBuilt) {
		t.Fatalf("Open() error = %v, want ErrRocksDBNotBuilt", err)
	}
	if !errors.Is(err, kvlite.ErrDriverUnavailable) {
		t.Fatalf("Open() error = %v, want ErrDriverUnavailable", err)
	}
}
