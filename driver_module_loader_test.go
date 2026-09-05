//go:build cgo

package kvlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenDriverFromRuntimeModule(t *testing.T) {
	db := openRuntimeModuleDB(t, t.TempDir())

	ctx := context.Background()
	if err := db.Put(ctx, "runtime:key", map[string]any{"hello": "module"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := db.Get(ctx, "runtime:key", &got); err != nil {
		t.Fatal(err)
	}
	if got["hello"] != "module" {
		t.Fatalf("Get() returned %#v, want map[hello:module]", got)
	}

	if err := db.Delete(ctx, "runtime:key"); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(ctx, "runtime:key", &got); err == nil {
		t.Fatal("Get() after Delete() expected error")
	}
}

func TestRuntimeModuleDriverRawRecordStore(t *testing.T) {
	db := openRuntimeModuleDB(t, t.TempDir())
	ctx := context.Background()
	store := db.Transport()

	records := map[string]string{
		"raw:a/1": "one",
		"raw:a/2": "two",
		"raw:b/1": "three",
	}
	for key, value := range records {
		if err := store.Put(ctx, []byte(key), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	for key, want := range records {
		got, found, err := store.Get(ctx, []byte(key))
		if err != nil || !found || string(got) != want {
			t.Fatalf("Transport().Get(%q) = %q, %t, %v; want %q, true, nil", key, got, found, err, want)
		}
	}
	if _, found, err := store.Get(ctx, []byte("raw:missing")); err != nil || found {
		t.Fatalf("Transport().Get(missing) = %t, %v; want false, nil", found, err)
	}

	// The engine keyspace is distinct from the logical keyspace: a raw record
	// must not leak into logical reads (and logical writes must not leak into
	// raw reads). Routing engine operations through the logical C ABI would
	// double-wrap records and break this isolation.
	var logical map[string]any
	if err := db.Get(ctx, "raw:a/1", &logical); err == nil {
		t.Fatal("logical Get() unexpectedly saw a raw engine record")
	}
	if err := db.Put(ctx, "raw:logical", map[string]any{"scope": "logical"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(ctx, []byte("raw:logical")); err != nil || found {
		t.Fatalf("Transport().Get(logical key) = %t, %v; want false, nil", found, err)
	}

	var scanned []string
	if err := store.ScanPrefix(ctx, []byte("raw:a/"), func(key, value []byte) error {
		scanned = append(scanned, string(key)+"="+string(value))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 2 || scanned[0] != "raw:a/1=one" || scanned[1] != "raw:a/2=two" {
		t.Fatalf("ScanPrefix(raw:a/) = %q, want [raw:a/1=one raw:a/2=two]", scanned)
	}

	scanned = nil
	if err := store.ScanPrefix(ctx, nil, func(key, _ []byte) error {
		scanned = append(scanned, string(key))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Three raw records plus the enveloped logical record's storage keys.
	if len(scanned) < 3 {
		t.Fatalf("empty-prefix scan returned %d keys, want at least 3", len(scanned))
	}

	if err := store.Delete(ctx, []byte("raw:a/1")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(ctx, []byte("raw:a/1")); err != nil || found {
		t.Fatalf("Get() after Delete() = %t, %v; want false, nil", found, err)
	}
}

// openRuntimeModuleDB builds the real capi C shared library, publishes it as
// an installed driver module, and opens a database through the production
// dlopen path. Every kvlite_raw_* symbol is exercised through this helper.
func openRuntimeModuleDB(t *testing.T, root string) *DB {
	t.Helper()
	tempDir := t.TempDir()
	moduleDirectory := filepath.Join(tempDir, string(DriverLevelDB))
	libName := testSharedLibraryName()
	artifactPath := filepath.Join(moduleDirectory, "lib", libName)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}

	buildPath := filepath.Join(tempDir, libName)
	buildSharedLibrary(t, buildPath)
	payload, err := os.ReadFile(buildPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, payload, 0o700); err != nil {
		t.Fatal(err)
	}

	checksum := sha256.Sum256(payload)
	manifest := testExtensionManifest(string(DriverLevelDB))
	manifest.Kind = ModuleKindDriver
	manifest.Driver = DriverLevelDB
	manifest.Artifacts = []ModuleArtifact{{
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Kind:     ModuleArtifactCShared,
		Path:     filepath.ToSlash(filepath.Join("lib", libName)),
		SHA256:   hex.EncodeToString(checksum[:]),
		Symbol:   "kvlite_abi_version",
	}}
	writeTestModuleManifest(t, moduleDirectory, manifest)

	t.Setenv("KVLITE_MODULE_PATH", tempDir)
	t.Setenv("KVLITE_HOME", "")

	db, err := Open(filepath.Join(root, "data"), WithDriver(string(DriverLevelDB)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testSharedLibraryName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libkvlite.dylib"
	case "windows":
		return "kvlite.dll"
	default:
		return "libkvlite.so"
	}
}

func buildSharedLibrary(t *testing.T, out string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-tags", "kvlite_leveldb", "-buildmode", "c-shared", "-o", out, "./capi")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build c-shared test module: %v: %s", err, output)
	}
}
