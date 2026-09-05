package kvlite

import (
	"context"
	"testing"
)

func TestMemoryDriverIsListedAndAvailable(t *testing.T) {
	info, err := DriverInfoFor(DriverMemory)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.Driver != DriverMemory {
		t.Fatalf("memory driver info = %#v, want an available memory driver", info)
	}
	if _, err := LinkedModule(string(DriverMemory)); err != nil {
		t.Fatalf("memory linked module = %v, want it registered", err)
	}
}

func TestMemoryDriverIsEphemeral(t *testing.T) {
	path := t.TempDir()
	ctx := context.Background()
	db, err := Open(path, WithDriver(string(DriverMemory)))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "scratch", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopening the same directory yields an empty database, never old
	// records: the manifest records the driver choice, the data dies with
	// the process handle.
	reopened, err := Open(path, WithDriver(string(DriverMemory)))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var got map[string]any
	if err := reopened.Get(ctx, "scratch", &got); err == nil {
		t.Fatal("Get() after reopen unexpectedly saw pre-close records")
	}
}

func TestMemoryDriverRejectsForeignDirectory(t *testing.T) {
	path := t.TempDir()
	db, err := Open(path, WithDriver(string(DriverMemory)))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	// A directory initialized for memory must not be adoptable by another
	// driver name, even a test one: the manifest still guards the boundary.
	if _, err := Open(path, WithDriver("test-memory")); err == nil {
		t.Fatal("Open(test-memory) over a memory directory unexpectedly succeeded")
	}
}
