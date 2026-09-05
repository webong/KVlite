//go:build !rocksdb

package kvlite

import (
	"errors"
	"testing"
)

// This binary registers the memory driver plus test fakes, so no single
// driver owns a bare Open: callers must still name their engine explicitly.
// (With memory alone registered, DefaultDriver resolves to it; see the
// CLI-level fallback test.)
func TestOpenRequiresExplicitDriverChoice(t *testing.T) {
	_, err := Open(t.TempDir())
	if !errors.Is(err, ErrDriverNotInstalled) {
		t.Fatalf("Open() error = %v, want ErrDriverNotInstalled", err)
	}

	_, err = Open(t.TempDir(), WithDriver("rocksdb"))
	if !errors.Is(err, ErrDriverNotInstalled) {
		t.Fatalf("Open(rocksdb) error = %v, want ErrDriverNotInstalled", err)
	}
}
