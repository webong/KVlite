package main

import "testing"

func TestABIVersionMatchesExport(t *testing.T) {
	if got := kvlite_abi_version(); got != abiVersion {
		t.Fatalf("kvlite_abi_version() = %d, want %d", got, abiVersion)
	}
}

func TestOpenWithBackendExportRejectsMissingArguments(t *testing.T) {
	if got := kvlite_open_with_backend(nil, nil, nil, nil); got != statusInvalid {
		t.Fatalf("kvlite_open_with_backend(nil, nil, nil, nil) = %d, want %d", got, statusInvalid)
	}
}

func TestOpenWithDriverExportRejectsMissingArguments(t *testing.T) {
	if got := kvlite_open_with_driver(nil, nil, nil, nil); got != statusInvalid {
		t.Fatalf("kvlite_open_with_driver(nil, nil, nil, nil) = %d, want %d", got, statusInvalid)
	}
}

// The raw record-store exports (kvlite_raw_put/get/delete/scan_open/scan_next/
// scan_close) are covered end to end through the runtime module driver loader
// test, which builds this package as a real C shared library and drives every
// raw symbol through the production dlopen path. They are not unit-tested
// here because Go does not support cgo imports in package-main test files.
