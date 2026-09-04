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

	dbPath := filepath.Join(t.TempDir(), "data")
	db, err := Open(dbPath, WithDriver(string(DriverLevelDB)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

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
