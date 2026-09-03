//go:build !berkeleydb || !cgo

package berkeleydb_test

import (
	"errors"
	"testing"

	"github.com/webong/kvlite"
	_ "github.com/webong/kvlite/extensions/berkeleydb"
)

func TestBerkeleyDBDriverReportsMissingNativeBuild(t *testing.T) {
	info, err := kvlite.DriverInfoFor(kvlite.DriverBerkeleyDB)
	if err != nil {
		t.Fatal(err)
	}
	if info.Available {
		t.Fatalf("Berkeley DB driver unexpectedly reports available: %#v", info)
	}
	_, err = kvlite.Open(t.TempDir(), kvlite.WithDriver("berkeleydb"))
	if !errors.Is(err, kvlite.ErrBerkeleyDBNotBuilt) {
		t.Fatalf("Open() error = %v, want ErrBerkeleyDBNotBuilt", err)
	}
	if !errors.Is(err, kvlite.ErrDriverUnavailable) {
		t.Fatalf("Open() error = %v, want ErrDriverUnavailable", err)
	}
}
