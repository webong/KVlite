package main

import (
	"testing"

	"github.com/webong/kvlite"
)

func TestRunRejectsUnavailableDriver(t *testing.T) {
	if got := run([]string{"serve", "--path", t.TempDir(), "--driver", "not-a-driver"}); got != 1 {
		t.Fatalf("run() = %d, want 1", got)
	}
}

func TestDriverPathValuesRejectDuplicateMappings(t *testing.T) {
	var values driverPathValues
	if err := values.Set("leveldb=./level"); err != nil {
		t.Fatal(err)
	}
	if got := values.items[kvlite.DriverLevelDB]; got != "./level" {
		t.Fatalf("leveldb path = %q", got)
	}
	if err := values.Set("leveldb=./other"); err == nil {
		t.Fatal("duplicate driver mapping did not fail")
	}
}

func TestDriverListCommandDoesNotRequireAStorageDriver(t *testing.T) {
	if got := run([]string{"driver", "list"}); got != 0 {
		t.Fatalf("run(driver list) = %d, want 0", got)
	}
}
