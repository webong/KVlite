package kvlite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestDiscoverModulesAndVerifyArtifacts(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "rocksdb")
	artifactPath := filepath.Join(directory, "lib", "libkvlite-driver-rocksdb.test")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("trusted prebuilt KVLite RocksDB module")
	if err := os.WriteFile(artifactPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	writeTestModuleManifest(t, directory, ModuleManifest{
		SchemaVersion: ModuleManifestVersion,
		Name:          "rocksdb",
		Kind:          ModuleKindDriver,
		Version:       "v0.1.0",
		ModuleABI:     ModuleABIVersion,
		Driver:        DriverRocksDB,
		Capabilities:  []string{"embedded-storage", "ttl-compaction"},
		Artifacts: []ModuleArtifact{{
			Platform: runtime.GOOS + "-" + runtime.GOARCH,
			Kind:     ModuleArtifactCShared,
			Path:     "lib/libkvlite-driver-rocksdb.test",
			SHA256:   hex.EncodeToString(digest[:]),
			Symbol:   "kvlite_abi_version",
		}},
		License: "Apache-2.0",
	})

	modules, err := DiscoverModules(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 1 {
		t.Fatalf("DiscoverModules() returned %#v", modules)
	}
	module := modules[0]
	if module.Linked || module.Manifest.Name != "rocksdb" || module.Directory != directory {
		t.Fatalf("unexpected discovered module: %#v", module)
	}
	if err := module.Verify(); err != nil {
		t.Fatal(err)
	}
	artifact, err := module.ArtifactForCurrentPlatform(ModuleArtifactCShared)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPath, err := module.ArtifactPath(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedPath != artifactPath {
		t.Fatalf("ArtifactPath() = %q, want %q", resolvedPath, artifactPath)
	}

	if err := os.WriteFile(artifactPath, []byte("modified after install"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := module.Verify(); !errors.Is(err, ErrModuleIntegrity) {
		t.Fatalf("Verify() error = %v, want ErrModuleIntegrity", err)
	}
}

func TestDiscoverModulesReadsDirectModuleDirectory(t *testing.T) {
	directory := t.TempDir()
	writeTestModuleManifest(t, directory, testExtensionManifest("http"))

	module, err := FindInstalledModule("http", directory)
	if err != nil {
		t.Fatal(err)
	}
	if module.Manifest.Kind != ModuleKindExtension || module.Manifest.Name != "http" {
		t.Fatalf("FindInstalledModule() = %#v", module)
	}
	if err := module.Verify(); !errors.Is(err, ErrModuleArtifactMissing) {
		t.Fatalf("Verify() error = %v, want ErrModuleArtifactMissing", err)
	}
}

func TestDiscoverModulesRejectsDuplicateNames(t *testing.T) {
	root := t.TempDir()
	writeTestModuleManifest(t, filepath.Join(root, "one"), testExtensionManifest("redis"))
	writeTestModuleManifest(t, filepath.Join(root, "two"), testExtensionManifest("redis"))
	_, err := DiscoverModules(root)
	if !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("DiscoverModules() error = %v, want ErrModuleConflict", err)
	}
}

func TestDiscoverModulesRejectsIncompatibleManifest(t *testing.T) {
	directory := t.TempDir()
	manifest := testExtensionManifest("http")
	manifest.ModuleABI++
	writeTestModuleManifest(t, directory, manifest)
	_, err := DiscoverModules(directory)
	if !errors.Is(err, ErrModuleIncompatible) {
		t.Fatalf("DiscoverModules() error = %v, want ErrModuleIncompatible", err)
	}
}

func TestModuleManifestRejectsUnsafeArtifactPath(t *testing.T) {
	manifest := testExtensionManifest("redis")
	manifest.Artifacts = []ModuleArtifact{{
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Kind:     ModuleArtifactExecutable,
		Path:     "../kvlite-redis",
	}}
	if err := manifest.Validate(); !errors.Is(err, ErrModuleManifestInvalid) {
		t.Fatalf("Validate() error = %v, want ErrModuleManifestInvalid", err)
	}
}

func TestModuleManifestRejectsAmbiguousArtifactSelection(t *testing.T) {
	manifest := testExtensionManifest("http")
	manifest.Artifacts = []ModuleArtifact{
		{
			Platform: runtime.GOOS + "-" + runtime.GOARCH,
			Kind:     ModuleArtifactExecutable,
			Path:     "bin/kvlite-http-a",
		},
		{
			Platform: runtime.GOOS + "-" + runtime.GOARCH,
			Kind:     ModuleArtifactExecutable,
			Path:     "bin/kvlite-http-b",
		},
	}
	if err := manifest.Validate(); !errors.Is(err, ErrModuleManifestInvalid) {
		t.Fatalf("Validate() error = %v, want ErrModuleManifestInvalid", err)
	}
}

func TestVerifyModuleRequiresArtifactChecksum(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "http")
	artifactPath := filepath.Join(directory, "bin", "kvlite-http")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("module"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := testExtensionManifest("http")
	manifest.Artifacts = []ModuleArtifact{{
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Kind:     ModuleArtifactExecutable,
		Path:     "bin/kvlite-http",
	}}
	writeTestModuleManifest(t, directory, manifest)
	module, err := FindInstalledModule("http", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Verify(); !errors.Is(err, ErrModuleIntegrity) {
		t.Fatalf("Verify() error = %v, want ErrModuleIntegrity", err)
	}
}

func TestDefaultModulePathsUsesConfiguredLocationsOnly(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	home := t.TempDir()
	t.Setenv("KVLITE_MODULE_PATH", first+string(os.PathListSeparator)+second+string(os.PathListSeparator)+first)
	t.Setenv("KVLITE_HOME", home)

	paths := DefaultModulePaths()
	want := []string{
		filepath.Clean(first),
		filepath.Clean(second),
		filepath.Join(home, "modules"),
		filepath.Join(home, "drivers"),
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("DefaultModulePaths() = %#v, want %#v", paths, want)
	}
	for _, candidate := range paths {
		if candidate == "." {
			t.Fatalf("DefaultModulePaths() unexpectedly includes current directory: %#v", paths)
		}
	}
}

func TestResolveModulePrefersInstalledArtifactThenLinkedModule(t *testing.T) {
	root := t.TempDir()
	writeTestModuleManifest(t, filepath.Join(root, "http"), testExtensionManifest("http"))
	module, err := ResolveModule("http", root)
	if err != nil {
		t.Fatal(err)
	}
	if module.Linked || module.Directory != filepath.Join(root, "http") {
		t.Fatalf("ResolveModule() = %#v, want installed artifact", module)
	}
	linkedManifest := testExtensionManifest("test-linked-resolver")
	if err := RegisterLinkedModule(linkedManifest); err != nil {
		t.Fatal(err)
	}
	module, err = ResolveModule("test-linked-resolver", root)
	if err != nil {
		t.Fatal(err)
	}
	if !module.Linked || module.Manifest.Name != "test-linked-resolver" {
		t.Fatalf("ResolveModule() = %#v, want linked module", module)
	}
	_, err = ResolveModule("not-installed", root)
	if !errors.Is(err, ErrModuleNotInstalled) {
		t.Fatalf("ResolveModule() error = %v, want ErrModuleNotInstalled", err)
	}
}

func TestResolveModuleExecutable(t *testing.T) {
	root := t.TempDir()
	executableDirectory := filepath.Join(root, "redis")
	executablePath := filepath.Join(executableDirectory, "bin", "kvlite-redis")
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("redis extension executable")
	if err := os.WriteFile(executablePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(payload)
	manifest := testExtensionManifest("redis")
	manifest.Artifacts = []ModuleArtifact{{
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Kind:     ModuleArtifactExecutable,
		Path:     filepath.ToSlash("bin/kvlite-redis"),
		SHA256:   hex.EncodeToString(checksum[:]),
	}}
	writeTestModuleManifest(t, executableDirectory, manifest)

	module, artifact, err := ResolveModuleExecutable("redis", root)
	if err != nil {
		t.Fatal(err)
	}
	if module.Manifest.Name != "redis" {
		t.Fatalf("ResolveModuleExecutable() module = %#v", module.Manifest)
	}
	if artifact.Kind != ModuleArtifactExecutable {
		t.Fatalf("ResolveModuleExecutable() artifact = %#v", artifact)
	}
	if artifact.Path != filepath.ToSlash("bin/kvlite-redis") {
		t.Fatalf("ResolveModuleExecutable() artifact = %#v, want path bin/kvlite-redis", artifact)
	}

	if err := os.WriteFile(executablePath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveModuleExecutable("redis", root); !errors.Is(err, ErrModuleIntegrity) {
		t.Fatalf("ResolveModuleExecutable() error = %v, want ErrModuleIntegrity", err)
	}

	linkedName := "standalone-test-module"
	linkedManifest := testExtensionManifest(linkedName)
	if err := RegisterLinkedModule(linkedManifest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveModuleExecutable(linkedName); !errors.Is(err, ErrModuleNoExecutable) {
		t.Fatalf("ResolveModuleExecutable() linked error = %v, want ErrModuleNoExecutable", err)
	}
}

func TestSourceModuleManifestsAreDiscoverable(t *testing.T) {
	modules, err := DiscoverModules("extensions")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(modules))
	for _, module := range modules {
		got = append(got, module.Manifest.Name)
	}
	want := []string{"berkeleydb", "http", "leveldb", "redis", "rocksdb"}
	if !slices.Equal(got, want) {
		t.Fatalf("source module names = %#v, want %#v", got, want)
	}
}

func testExtensionManifest(name string) ModuleManifest {
	return ModuleManifest{
		SchemaVersion: ModuleManifestVersion,
		Name:          name,
		Kind:          ModuleKindExtension,
		Version:       "v0.1.0",
		ModuleABI:     ModuleABIVersion,
		Capabilities:  []string{"network-server"},
		License:       "Apache-2.0",
	}
}

func writeTestModuleManifest(t *testing.T, directory string, manifest ModuleManifest) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ModuleManifestFilename), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
