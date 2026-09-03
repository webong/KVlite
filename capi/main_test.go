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
