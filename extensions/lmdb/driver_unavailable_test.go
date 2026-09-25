//go:build !cgo

package lmdb_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/webong/kvlite"
	_ "github.com/webong/kvlite/extensions/lmdb"
)

func TestUnavailableWithoutCGO(t *testing.T) {
	_, err := kvlite.Open(t.TempDir(), kvlite.WithDriver("lmdb"))
	if err == nil || !strings.Contains(err.Error(), "CGO_ENABLED=1") {
		t.Fatalf("Open() error = %v, want actionable CGO requirement", err)
	}
	if errors.Is(err, kvlite.ErrDriverNotInstalled) {
		t.Fatalf("registered LMDB extension should report unavailable, not missing: %v", err)
	}
}
