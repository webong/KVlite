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

func withLinkedExtensionProbes(t *testing.T, httpLinked, redisLinked bool) func() {
	t.Helper()
	origHTTPExtensionLinked := isHTTPExtensionLinked
	origRedisExtensionLinked := isRedisExtensionLinked
	isHTTPExtensionLinked = func() bool { return httpLinked }
	isRedisExtensionLinked = func() bool { return redisLinked }
	return func() {
		isHTTPExtensionLinked = origHTTPExtensionLinked
		isRedisExtensionLinked = origRedisExtensionLinked
	}
}

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

func TestServeStandaloneModeRunsHTTPModule(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("standalone module execution is not supported")
	}
	root := t.TempDir()
	httpDirectory := filepath.Join(root, "http")
	sourcePath := filepath.Join(httpDirectory, "main.go")
	binaryName := "kvlite-http"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(httpDirectory, "bin", binaryName)
	markerPath := filepath.Join(root, "serve-module-marker.txt")

	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	program := fmt.Sprintf(`
package main

import (
	"flag"
	"os"
)

func main() {
	flags := flag.NewFlagSet("kvlite-http", flag.ContinueOnError)
	path := flags.String("path", "", "")
	_ = flags.String("listen", "", "")
	_ = flags.String("driver", "", "")
	_ = flags.String("token", "", "")
	_ = flags.Int64("max-request-bytes", 0, "")
	if err := flags.Parse(os.Args[1:]); err != nil {
		panic(err)
	}
	if *path == "" {
		panic("path is required")
	}
	_ = os.WriteFile(%q, []byte(*path), 0o600)
}
`, markerPath)
	if err := os.WriteFile(sourcePath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", binaryPath, sourcePath).CombinedOutput(); err != nil {
		t.Fatalf("go build http module executable: %v: %s", err, out)
	}

	payload, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	manifestPath := filepath.Join(httpDirectory, kvlite.ModuleManifestFilename)
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "name": "http",
  "kind": "extension",
  "version": "v0.1.0",
  "module_abi": 1,
  "capabilities": ["http-client", "http-server"],
  "license": "Apache-2.0",
  "artifacts": [{
    "platform": "`+runtime.GOOS+`-`+runtime.GOARCH+`",
    "kind": "executable",
    "path": "bin/%s",
    "sha256": "%s"
  }]
}`, binaryName, hex.EncodeToString(checksum[:]))
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	dataPath := filepath.Join(root, "data")
	if got := run([]string{"serve", "--path", dataPath, "--extension-mode", "standalone", "--listen", "127.0.0.1:8089"}); got != 0 {
		t.Fatalf("run(serve standalone http) = %d, want 0", got)
	}
	written, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("standalone http marker missing: %v", err)
	}
	if string(written) != dataPath {
		t.Fatalf("standalone http marker contains %q, want %q", string(written), dataPath)
	}
}

func TestServeAutoFallsBackToStandaloneHTTPWhenLinkedHTTPMissing(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("standalone module execution is not supported")
	}
	restore := withLinkedExtensionProbes(t, false, false)
	defer restore()

	root := t.TempDir()
	httpDirectory := filepath.Join(root, "http")
	sourcePath := filepath.Join(httpDirectory, "main.go")
	binaryName := "kvlite-http"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(httpDirectory, "bin", binaryName)
	markerPath := filepath.Join(root, "serve-auto-module-marker.txt")

	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	program := fmt.Sprintf(`
package main

import (
	"flag"
	"os"
)

func main() {
	flags := flag.NewFlagSet("kvlite-http", flag.ContinueOnError)
	path := flags.String("path", "", "")
	_ = flags.String("listen", "", "")
	_ = flags.String("driver", "", "")
	_ = flags.String("token", "", "")
	_ = flags.Int64("max-request-bytes", 0, "")
	if err := flags.Parse(os.Args[1:]); err != nil {
		panic(err)
	}
	if *path == "" {
		panic("path is required")
	}
	_ = os.WriteFile(%q, []byte(*path), 0o600)
}
`, markerPath)
	if err := os.WriteFile(sourcePath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", binaryPath, sourcePath).CombinedOutput(); err != nil {
		t.Fatalf("go build http module executable: %v: %s", err, out)
	}

	payload, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	manifestPath := filepath.Join(httpDirectory, kvlite.ModuleManifestFilename)
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "name": "http",
  "kind": "extension",
  "version": "v0.1.0",
  "module_abi": 1,
  "capabilities": ["http-client", "http-server"],
  "license": "Apache-2.0",
  "artifacts": [{
    "platform": "`+runtime.GOOS+`-`+runtime.GOARCH+`",
    "kind": "executable",
    "path": "bin/%s",
    "sha256": "%s"
  }]
}`, binaryName, hex.EncodeToString(checksum[:]))
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	dataPath := filepath.Join(root, "data")
	if got := run([]string{"serve", "--path", dataPath, "--extension-mode", "auto", "--listen", "127.0.0.1:8089"}); got != 0 {
		t.Fatalf("run(serve auto fallback http) = %d, want 0", got)
	}
	written, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("standalone http marker missing: %v", err)
	}
	if string(written) != dataPath {
		t.Fatalf("standalone http marker contains %q, want %q", string(written), dataPath)
	}
}

func TestServeStandaloneModeRejectsHTTPAndRedisTogether(t *testing.T) {
	if got := run([]string{"serve", "--path", t.TempDir(), "--extension-mode", "standalone", "--listen", "127.0.0.1:8089", "--redis-listen", "127.0.0.1:6379"}); got != 1 {
		t.Fatalf("run(serve standalone both) = %d, want 1", got)
	}
}

func TestServeStandaloneModeRejectsUnknownMode(t *testing.T) {
	if got := run([]string{"serve", "--path", t.TempDir(), "--extension-mode", "unknown"}); got != 2 {
		t.Fatalf("run(serve unknown mode) = %d, want 2", got)
	}
}

func TestServeStandaloneModeRunsRedisModule(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("standalone module execution is not supported")
	}
	root := t.TempDir()
	moduleDirectory := filepath.Join(root, "redis")
	sourcePath := filepath.Join(moduleDirectory, "main.go")
	binaryName := "kvlite-redis"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(moduleDirectory, "bin", binaryName)
	markerPath := filepath.Join(root, "serve-redis-marker.txt")

	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	program := fmt.Sprintf(`
package main

import (
	"flag"
	"os"
)

func main() {
	flags := flag.NewFlagSet("kvlite-redis", flag.ContinueOnError)
	path := flags.String("path", "", "")
	_ = flags.String("listen", "", "")
	_ = flags.String("driver", "", "")
	_ = flags.String("password", "", "")
	if err := flags.Parse(os.Args[1:]); err != nil {
		panic(err)
	}
	if *path == "" {
		panic("path is required")
	}
	_ = os.WriteFile(%q, []byte(*path), 0o600)
}
`, markerPath)
	if err := os.WriteFile(sourcePath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", binaryPath, sourcePath).CombinedOutput(); err != nil {
		t.Fatalf("go build redis module executable: %v: %s", err, out)
	}

	payload, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	manifestPath := filepath.Join(moduleDirectory, kvlite.ModuleManifestFilename)
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "name": "redis",
  "kind": "extension",
  "version": "v0.1.0",
  "module_abi": 1,
  "capabilities": ["redis-resp2", "redis-server"],
  "license": "Apache-2.0",
  "artifacts": [{
    "platform": "`+runtime.GOOS+`-`+runtime.GOARCH+`",
    "kind": "executable",
    "path": "bin/%s",
    "sha256": "%s"
  }]
}`, binaryName, hex.EncodeToString(checksum[:]))
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	dataPath := filepath.Join(root, "data")
	if got := run([]string{"serve", "--path", dataPath, "--extension-mode", "standalone", "--redis-listen", "127.0.0.1:6379"}); got != 0 {
		t.Fatalf("run(serve standalone redis) = %d, want 0", got)
	}
	written, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("standalone redis marker missing: %v", err)
	}
	if string(written) != dataPath {
		t.Fatalf("standalone redis marker contains %q, want %q", string(written), dataPath)
	}
}

func TestServeAutoRejectsRedisWithoutLinkedRedis(t *testing.T) {
	restore := withLinkedExtensionProbes(t, false, false)
	defer restore()

	if got := run([]string{"serve", "--path", t.TempDir(), "--extension-mode", "auto", "--redis-listen", "127.0.0.1:6379"}); got != 1 {
		t.Fatalf("run(serve auto redis no linked redis) = %d, want 1", got)
	}
}

func TestServeAutoRejectsDriverPathWithoutLinkedHTTP(t *testing.T) {
	restore := withLinkedExtensionProbes(t, false, true)
	defer restore()

	if got := run([]string{"serve", "--path", t.TempDir(), "--extension-mode", "auto", "--listen", "127.0.0.1:8089", "--driver-path", "rocksdb=/tmp"}); got != 1 {
		t.Fatalf("run(serve auto driver-path fallback) = %d, want 1", got)
	}
}
