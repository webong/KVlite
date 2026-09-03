package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestModuleListShowsLinkedModuleMetadata(t *testing.T) {
	if got := run([]string{"module", "list"}); got != 0 {
		t.Fatalf("run(module list) = %d, want 0", got)
	}
}

func TestModuleCommandRejectsUnknownSubcommand(t *testing.T) {
	if got := run([]string{"module", "install"}); got != 2 {
		t.Fatalf("run(module install) = %d, want 2", got)
	}
}

func TestModuleVerifyChecksDiscoveredBundle(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "leveldb")
	artifact := filepath.Join(directory, "lib", "libkvlite.test")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("checked LevelDB module")
	if err := os.WriteFile(artifact, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	manifest := []byte(`{
  "schema_version": 1,
  "name": "leveldb",
  "kind": "driver",
  "version": "v0.1.0",
  "module_abi": 1,
  "driver": "leveldb",
  "capabilities": ["embedded-storage"],
  "license": "Apache-2.0",
  "artifacts": [{
    "platform": "` + runtime.GOOS + `-` + runtime.GOARCH + `",
    "kind": "c-shared",
    "path": "lib/libkvlite.test",
    "sha256": "` + hex.EncodeToString(digest[:]) + `"
  }]
}`)
	if err := os.WriteFile(filepath.Join(directory, kvlite.ModuleManifestFilename), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	if got := run([]string{"module", "verify", "leveldb"}); got != 0 {
		t.Fatalf("run(module verify leveldb) = %d, want 0", got)
	}
	if err := os.WriteFile(artifact, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := run([]string{"module", "verify", "leveldb"}); got != 1 {
		t.Fatalf("run(module verify leveldb after tamper) = %d, want 1", got)
	}
}

func TestModuleRunExecUsesDiscoveredExecutable(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("standalone module execution is not supported")
	}
	root := t.TempDir()
	executableDirectory := filepath.Join(root, "redis")
	source := filepath.Join(executableDirectory, "main.go")
	binaryName := "kvlite-redis"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(executableDirectory, "bin", binaryName)
	markerPath := filepath.Join(root, "module-run-marker.txt")

	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	program := fmt.Sprintf(`
package main

import (
	"os"
)

func main() {
	_ = os.WriteFile(%q, []byte("ran"), 0o600)
}
`, markerPath)
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", binaryPath, source).CombinedOutput(); err != nil {
		t.Fatalf("go build module executable: %v: %s", err, out)
	}

	payload, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	manifestPath := filepath.Join(executableDirectory, "kvlite-module.json")
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "name": "redis",
  "kind": "extension",
  "version": "v0.1.0",
  "module_abi": 1,
  "capabilities": ["redis-resp2", "redis-server"],
  "license": "Apache-2.0",
  "artifacts": [{
    "platform": "` + runtime.GOOS + `-` + runtime.GOARCH + `",
    "kind": "executable",
    "path": "bin/` + binaryName + `",
    "sha256": "` + hex.EncodeToString(checksum[:]) + `"
  }]
}`)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	if got := run([]string{"module", "run", "redis"}); got != 0 {
		t.Fatalf("run(module run redis) = %d, want 0", got)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("module executable marker missing: %v", err)
	}
}
