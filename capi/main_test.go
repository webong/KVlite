package main

import "testing"

func TestABIVersionMatchesExport(t *testing.T) {
	if got := kvlite_abi_version(); got != abiVersion {
		t.Fatalf("kvlite_abi_version() = %d, want %d", got, abiVersion)
	}
}
