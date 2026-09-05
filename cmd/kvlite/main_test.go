package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/webong/kvlite"
	kvliteredis "github.com/webong/kvlite/extensions/redis"
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

func TestServeAutoFallsBackToStandaloneRedisWhenLinkedRedisMissing(t *testing.T) {
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
	markerPath := filepath.Join(root, "serve-auto-redis-module-marker.txt")

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

	restore := withLinkedExtensionProbes(t, false, false)
	defer restore()

	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	dataPath := filepath.Join(root, "data")
	if got := run([]string{"serve", "--path", dataPath, "--extension-mode", "auto", "--redis-listen", "127.0.0.1:6379"}); got != 0 {
		t.Fatalf("run(serve auto redis) = %d, want 0", got)
	}
	written, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("standalone redis marker missing: %v", err)
	}
	if string(written) != dataPath {
		t.Fatalf("standalone redis marker contains %q, want %q", string(written), dataPath)
	}
}

func TestServeStandaloneModeRunsHTTPOwnerWithAttachedRedis(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("standalone module execution is not supported")
	}
	root := t.TempDir()
	httpMarkerPath := filepath.Join(root, "serve-both-http-marker.txt")
	redisMarkerPath := filepath.Join(root, "serve-both-redis-marker.txt")

	ownerProgram := fmt.Sprintf(`
package main

import (
	"flag"
	"net/http"
	"os"
)

func main() {
	flags := flag.NewFlagSet("kvlite-http", flag.ContinueOnError)
	path := flags.String("path", "", "")
	listen := flags.String("listen", "", "")
	_ = flags.String("driver", "", "")
	_ = flags.String("token", "", "")
	_ = flags.Int64("max-request-bytes", 0, "")
	if err := flags.Parse(os.Args[1:]); err != nil {
		panic(err)
	}
	if *path == "" || *listen == "" {
		panic("path and listen are required")
	}
	if err := os.WriteFile(%q, []byte(*path), 0o600); err != nil {
		panic(err)
	}
	http.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(%q))
	})
	if err := http.ListenAndServe(*listen, nil); err != nil {
		panic(err)
	}
}
`, httpMarkerPath, `{"status":"ok"}`)
	buildFakeModule(t, root, "http", []string{"http-client", "http-server"}, ownerProgram)

	attachedProgram := fmt.Sprintf(`
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
)

func main() {
	flags := flag.NewFlagSet("kvlite-redis", flag.ContinueOnError)
	upstream := flags.String("upstream", "", "")
	_ = flags.String("listen", "", "")
	_ = flags.String("upstream-token", "", "")
	_ = flags.String("upstream-driver", "", "")
	_ = flags.String("password", "", "")
	if err := flags.Parse(os.Args[1:]); err != nil {
		panic(err)
	}
	if *upstream == "" {
		panic("upstream is required")
	}
	response, err := http.Get(*upstream + "/v1/health")
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("owner status = %%s", response.Status))
	}
	if err := os.WriteFile(%q, []byte(*upstream), 0o600); err != nil {
		panic(err)
	}
}
`, redisMarkerPath)
	buildFakeModule(t, root, "redis", []string{"redis-resp2", "redis-server"}, attachedProgram)

	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	dataPath := filepath.Join(root, "data")
	ownerListen := "127.0.0.1:18091"
	if got := run([]string{"serve", "--path", dataPath, "--extension-mode", "standalone", "--listen", ownerListen, "--redis-listen", "127.0.0.1:16391"}); got != 0 {
		t.Fatalf("run(serve standalone both) = %d, want 0", got)
	}
	written, err := os.ReadFile(httpMarkerPath)
	if err != nil {
		t.Fatalf("owner http marker missing: %v", err)
	}
	if string(written) != dataPath {
		t.Fatalf("owner http marker contains %q, want %q", string(written), dataPath)
	}
	written, err = os.ReadFile(redisMarkerPath)
	if err != nil {
		t.Fatalf("attached redis marker missing: %v", err)
	}
	if string(written) != "http://"+ownerListen {
		t.Fatalf("attached redis marker contains %q, want %q", string(written), "http://"+ownerListen)
	}
}

func TestServeStandaloneBothRequiresExplicitPorts(t *testing.T) {
	// An ephemeral owner port cannot be discovered through child stdout, so
	// the combined topology asks for explicit addresses instead of starting
	// half a topology.
	if got := run([]string{"serve", "--path", t.TempDir(), "--extension-mode", "standalone", "--listen", "127.0.0.1:0", "--redis-listen", "127.0.0.1:6379"}); got != 2 {
		t.Fatalf("run(serve standalone both with :0) = %d, want 2", got)
	}
}

func TestMemoryDriverAvailableWithoutLinkedDrivers(t *testing.T) {
	// This binary links no storage driver, so the only registered engine is
	// the built-in ephemeral one. The library keeps its explicit RocksDB
	// compatibility default (a bare Open must never silently hand out
	// throwaway storage), while an explicit memory selection works with
	// zero installs and the CLI default resolves to memory.
	drivers := kvlite.Drivers()
	if len(drivers) != 1 {
		t.Skip("linked drivers present; memory-only registry is shadowed")
	}
	if drivers[0].Driver != kvlite.DriverMemory || !drivers[0].Available {
		t.Fatalf("sole linked driver = %#v, want an available memory driver", drivers[0])
	}
	if got := kvlite.DefaultDriver(); got != kvlite.DriverMemory {
		t.Fatalf("DefaultDriver() = %q, want %q", got, kvlite.DriverMemory)
	}
	if _, err := kvlite.Open(t.TempDir()); !errors.Is(err, kvlite.ErrDriverNotInstalled) {
		t.Fatalf("bare Open() = %v, want ErrDriverNotInstalled (compat default preserved)", err)
	}
	path := t.TempDir()
	db, err := kvlite.Open(path, kvlite.WithDriver("memory"))
	if err != nil {
		t.Fatalf("Open(memory) = %v, want an ephemeral database", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Put(ctx, "scratch", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := db.Get(ctx, "scratch", &got); err != nil {
		t.Fatal(err)
	}
}

func TestAttachedRedisRejectsEmbeddedOwner(t *testing.T) {
	// Needs a real embedded database; run with -tags kvlite_leveldb.
	db, err := kvlite.Open(t.TempDir(), kvlite.WithDriver("leveldb"))
	if err != nil {
		t.Skipf("leveldb driver is not linked into this test binary: %v", err)
	}
	defer db.Close()
	if db.IsRemote() {
		t.Fatal("embedded database is unexpectedly marked remote")
	}
	// The attached topology must refuse an embedded handle at the API
	// boundary instead of serving a second owner for one directory.
	if _, err := kvliteredis.ServeRemote(db, kvliteredis.Options{ListenAddress: "127.0.0.1:0"}); err == nil {
		t.Fatal("ServeRemote over an embedded database unexpectedly succeeded")
	}
}

// buildFakeModule compiles a small standalone program and publishes it as an
// installed executable module for discovery through KVLITE_MODULE_PATH.
func buildFakeModule(t *testing.T, root, name string, capabilities []string, program string) {
	t.Helper()
	directory := filepath.Join(root, name)
	sourcePath := filepath.Join(directory, "main.go")
	binaryName := "kvlite-" + name
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(directory, "bin", binaryName)
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", binaryPath, sourcePath).CombinedOutput(); err != nil {
		t.Fatalf("go build %s module executable: %v: %s", name, err, out)
	}
	payload, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "name": %q,
  "kind": "extension",
  "version": "v0.1.0",
  "module_abi": 1,
  "capabilities": %s,
  "license": "Apache-2.0",
  "artifacts": [{
    "platform": "`+runtime.GOOS+`-`+runtime.GOARCH+`",
    "kind": "executable",
    "path": "bin/%s",
    "sha256": "%s"
  }]
}`, name, capabilitiesJSON, binaryName, hex.EncodeToString(checksum[:]))
	if err := os.WriteFile(filepath.Join(directory, kvlite.ModuleManifestFilename), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
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
