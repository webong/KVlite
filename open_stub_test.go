//go:build !rocksdb

package kvlite

import (
	"errors"
	"testing"
)

func TestOpenRequiresRocksDBBuildTag(t *testing.T) {
	_, err := Open(t.TempDir())
	if !errors.Is(err, ErrRocksDBNotBuilt) {
		t.Fatalf("Open() error = %v, want ErrRocksDBNotBuilt", err)
	}
}
