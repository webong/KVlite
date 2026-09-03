//go:build !berkeleydb || !cgo

package berkeleydb

import "github.com/webong/kvlite"

func nativeAvailable() error {
	return kvlite.ErrBerkeleyDBNotBuilt
}

func openNative(_ string, _ kvlite.DriverOptions) (kvlite.Engine, error) {
	return nil, kvlite.ErrBerkeleyDBNotBuilt
}
