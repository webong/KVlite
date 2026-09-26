// Package lmdb registers KVLite's optional LMDB driver. It requires CGO and
// uses the LMDB 0.9 C source bundled by github.com/PowerDNS/lmdb-go.
package lmdb

import "github.com/webong/kvlite"

const Name kvlite.DriverName = kvlite.DriverLMDB

type driver struct{}

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
	kvlite.MustRegisterDriver(driver{})
}

func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          string(Name), Kind: kvlite.ModuleKindEngine,
		Version: "v0.1.0", ModuleABI: kvlite.ModuleABIVersion,
		Driver: Name, Capabilities: []string{"embedded-storage", "native-cgo"}, License: "BSD-3-Clause AND OLDAP-2.8",
	}
}

func (driver) Info() kvlite.DriverInfo {
	return kvlite.DriverInfo{Driver: Name, Implementation: "powerdns-lmdb-go", Format: "lmdb-0.9-v1", Version: "v1.9.4"}
}

func (driver) Available() error { return nativeAvailable() }

func (driver) Open(path string, _ kvlite.DriverOptions) (kvlite.Engine, error) {
	if err := nativeAvailable(); err != nil {
		return nil, err
	}
	return openNative(path)
}
