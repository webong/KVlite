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
	"strings"
	"testing"
)

// TestNativeModuleDriverLoad exercises the frozen kvlite_module_init_v1
// contract end to end: a pure-C reference module (no Go toolchain involved)
// is compiled, published as an installed native-module driver, opened through
// Open, and driven through logical, raw, and collection APIs.
func TestNativeModuleDriverLoad(t *testing.T) {
	artifactPath := buildNativeFixture(t)
	moduleDirectory := filepath.Join(t.TempDir(), "memdb")
	if err := os.MkdirAll(moduleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	relPath := "memdb" + nativeFixtureExtension()
	if err := os.WriteFile(filepath.Join(moduleDirectory, relPath), payload, 0o700); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	manifest := testExtensionManifest("memdb")
	manifest.Kind = ModuleKindDriver
	manifest.Driver = "memdb"
	manifest.Artifacts = []ModuleArtifact{{
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Kind:     ModuleArtifactNative,
		Path:     relPath,
		SHA256:   hex.EncodeToString(checksum[:]),
		Symbol:   "kvlite_module_init_v1",
	}}
	writeTestModuleManifest(t, moduleDirectory, manifest)

	t.Setenv("KVLITE_MODULE_PATH", filepath.Dir(moduleDirectory))
	t.Setenv("KVLITE_HOME", "")

	dbPath := filepath.Join(t.TempDir(), "data")
	db, err := Open(dbPath, WithDriver("memdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := db.Put(ctx, "native:key", map[string]any{"hello": "native"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := db.Get(ctx, "native:key", &got); err != nil {
		t.Fatal(err)
	}
	if got["hello"] != "native" {
		t.Fatalf("Get() returned %#v, want map[hello:native]", got)
	}

	store := db.Transport()
	if err := store.Put(ctx, []byte("native:raw/1"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, []byte("native:raw/2"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	var scanned []string
	if err := store.ScanPrefix(ctx, []byte("native:raw/"), func(key, value []byte) error {
		scanned = append(scanned, string(key)+"="+string(value))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(scanned) != 2 {
		t.Fatalf("ScanPrefix() returned %d items, want 2", len(scanned))
	}

	if err := db.HSet(ctx, "native:hash", "field", "value"); err != nil {
		t.Fatal(err)
	}
	var field string
	if err := db.HGet(ctx, "native:hash", "field", &field); err != nil {
		t.Fatal(err)
	}
	if field != "value" {
		t.Fatalf("HGet() = %q, want %q", field, "value")
	}

	if err := db.Delete(ctx, "native:key"); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(ctx, "native:key", &got); err == nil {
		t.Fatal("Get() after Delete() expected error")
	}
}

func TestNativeModuleDriverRejectsUnknownSymbol(t *testing.T) {
	artifactPath := buildNativeFixture(t)
	moduleDirectory := filepath.Join(t.TempDir(), "memdb")
	if err := os.MkdirAll(moduleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	relPath := "memdb" + nativeFixtureExtension()
	if err := os.WriteFile(filepath.Join(moduleDirectory, relPath), payload, 0o700); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	manifest := testExtensionManifest("memdb-bad-symbol")
	manifest.Kind = ModuleKindDriver
	manifest.Driver = "memdb-bad-symbol"
	manifest.Artifacts = []ModuleArtifact{{
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Kind:     ModuleArtifactNative,
		Path:     relPath,
		SHA256:   hex.EncodeToString(checksum[:]),
		Symbol:   "kvlite_module_init_v1",
	}}
	// Rewrite the manifest with a symbol the fixture does not export. The
	// manifest stays schema-valid; loading must fail at symbol resolution.
	manifest.Artifacts[0].Symbol = "kvlite_module_init_v9"
	writeTestModuleManifest(t, moduleDirectory, manifest)

	t.Setenv("KVLITE_MODULE_PATH", filepath.Dir(moduleDirectory))
	t.Setenv("KVLITE_HOME", "")

	_, err = Open(filepath.Join(t.TempDir(), "data"), WithDriver("memdb-bad-symbol"))
	if err == nil {
		t.Fatal("Open() with an unknown init symbol unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "kvlite_module_init_v9") {
		t.Fatalf("Open() error = %v, want it to name the unknown symbol", err)
	}
}

func nativeFixtureExtension() string {
	switch runtime.GOOS {
	case "darwin":
		return ".dylib"
	case "windows":
		return ".dll"
	default:
		return ".so"
	}
}

// buildNativeFixture compiles the pure-C reference module with the system C
// compiler and returns the shared-library path. It skips when no compiler is
// available.
func buildNativeFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("system C compiler (cc) is not available")
	}
	source := filepath.Join("capi", "testdata", "nativememdb", "memdb.c")
	output := filepath.Join(t.TempDir(), "memdb"+nativeFixtureExtension())
	args := []string{"-shared", "-fPIC", "-O2", "-o", output, source}
	if runtime.GOOS == "darwin" {
		args = []string{"-dynamiclib", "-fPIC", "-O2", "-o", output, source}
	}
	if output, err := exec.Command("cc", args...).CombinedOutput(); err != nil {
		t.Skipf("system C compiler cannot build the fixture: %v: %s", err, output)
	}
	return output
}
