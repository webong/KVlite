//go:build !rocksdb

package kvlite

import (
	"errors"
	"testing"
)

func TestOpenRequiresInstalledDefaultDriver(t *testing.T) {
	_, err := Open(t.TempDir())
	if !errors.Is(err, ErrDriverNotInstalled) {
		t.Fatalf("Open() error = %v, want ErrDriverNotInstalled", err)
	}
}
