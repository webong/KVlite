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
		Kind:          ModuleKindEngine,
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
	if module.Manifest.Kind != ModuleKindTransport || module.Manifest.Name != "http" {
		t.Fatalf("FindInstalledModule() = %#v", module)
	}
	if err := module.Verify(); !errors.Is(err, ErrModuleArtifactMissing) {
		t.Fatalf("Verify() error = %v, want ErrModuleArtifactMissing", err)
	}
}

func TestModuleKindSeparatesEngineAndTransport(t *testing.T) {
	engine := testExtensionManifest("rocksdb")
	engine.Kind = ModuleKindEngine
	engine.Driver = DriverRocksDB
	if err := engine.Validate(); err != nil {
		t.Fatal(err)
	}
	transport := testExtensionManifest("http")
	if err := transport.Validate(); err != nil {
		t.Fatal(err)
	}
	transport.Driver = DriverRocksDB
	if err := transport.Validate(); !errors.Is(err, ErrModuleManifestInvalid) {
		t.Fatalf("transport with driver error = %v, want ErrModuleManifestInvalid", err)
	}
	engine.Driver = ""
	if err := engine.Validate(); !errors.Is(err, ErrModuleManifestInvalid) {
		t.Fatalf("engine without driver error = %v, want ErrModuleManifestInvalid", err)
	}
}

func TestMultiKindExtensionProvidesEngineAndTransport(t *testing.T) {
	root := t.TempDir()
	manifest := testExtensionManifest("combo")
	manifest.SchemaVersion = ModuleManifestMultiKindVersion
	manifest.Kind = ""
	manifest.Kinds = []ModuleKind{ModuleKindEngine, ModuleKindTransport}
	manifest.Driver = "combo-engine"
	manifest.Artifacts = []ModuleArtifact{
		{Platform: runtime.GOOS + "-" + runtime.GOARCH, Kind: ModuleArtifactCShared, Path: "lib/engine.test"},
		{Platform: runtime.GOOS + "-" + runtime.GOARCH, Kind: ModuleArtifactExecutable, Path: "bin/transport.test"},
	}
	for index := range manifest.Artifacts {
		artifact := &manifest.Artifacts[index]
		artifactPath := filepath.Join(root, manifest.Name, filepath.FromSlash(artifact.Path))
		if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
			t.Fatal(err)
		}
		payload := []byte(artifact.Kind)
		if err := os.WriteFile(artifactPath, payload, 0o700); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		artifact.SHA256 = hex.EncodeToString(digest[:])
	}
	writeTestModuleManifest(t, filepath.Join(root, manifest.Name), manifest)

	module, err := FindInstalledModule("combo", root)
	if err != nil {
		t.Fatal(err)
	}
	if !module.Manifest.Provides(ModuleKindEngine) || !module.Manifest.Provides(ModuleKindTransport) {
		t.Fatalf("multi-kind extension lost a capability: %#v", module.Manifest)
	}
	kinds := module.Manifest.ProvidedKinds()
	kinds[0] = ModuleKindTransport
	if !module.Manifest.Provides(ModuleKindEngine) {
		t.Fatal("ProvidedKinds exposed mutable manifest metadata")
	}
	if err := module.Verify(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveModuleExecutable("combo", root); err != nil {
		t.Fatalf("transport artifact cannot be resolved: %v", err)
	}
	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	t.Setenv("KVLITE_SYSTEM_MODULE_PATH", "")
	driverModule, err := resolveModuleForDriver("combo-engine")
	if err != nil {
		t.Fatalf("engine driver cannot be resolved: %v", err)
	}
	if driverModule.Manifest.Name != "combo" {
		t.Fatalf("engine driver resolved %q, want combo", driverModule.Manifest.Name)
	}
}

func TestResolveModuleForDriverRejectsDuplicateClaims(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first", "second"} {
		manifest := testExtensionManifest(name)
		manifest.SchemaVersion = ModuleManifestMultiKindVersion
		manifest.Kind = ""
		manifest.Kinds = []ModuleKind{ModuleKindEngine, ModuleKindTransport}
		manifest.Driver = "same-engine"
		writeTestModuleManifest(t, filepath.Join(root, name), manifest)
	}
	t.Setenv("KVLITE_MODULE_PATH", root)
	t.Setenv("KVLITE_HOME", "")
	t.Setenv("KVLITE_SYSTEM_MODULE_PATH", "")
	if _, err := resolveModuleForDriver("same-engine"); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("resolveModuleForDriver() error = %v, want ErrModuleConflict", err)
	}
}

func TestMultiKindManifestRejectsInvalidDeclarations(t *testing.T) {
	valid := testExtensionManifest("combo")
	valid.SchemaVersion = ModuleManifestMultiKindVersion
	valid.Kind = ""
	valid.Kinds = []ModuleKind{ModuleKindEngine, ModuleKindTransport}
	valid.Driver = "combo-engine"
	for _, test := range []struct {
		name   string
		mutate func(*ModuleManifest)
	}{
		{"missing kinds", func(manifest *ModuleManifest) { manifest.Kinds = nil }},
		{"both kind fields", func(manifest *ModuleManifest) { manifest.Kind = ModuleKindEngine }},
		{"duplicate kind", func(manifest *ModuleManifest) { manifest.Kinds = []ModuleKind{ModuleKindEngine, ModuleKindEngine} }},
		{"legacy kind in v2", func(manifest *ModuleManifest) { manifest.Kinds[0] = "driver" }},
		{"engine without driver", func(manifest *ModuleManifest) { manifest.Driver = "" }},
		{"transport only with driver", func(manifest *ModuleManifest) { manifest.Kinds = []ModuleKind{ModuleKindTransport} }},
		{"v1 with kinds", func(manifest *ModuleManifest) { manifest.SchemaVersion = ModuleManifestVersion }},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneModuleManifest(valid)
			test.mutate(&manifest)
			if err := manifest.Validate(); !errors.Is(err, ErrModuleManifestInvalid) {
				t.Fatalf("Validate() error = %v, want ErrModuleManifestInvalid", err)
			}
		})
	}
}

func TestLegacyModuleKindsNormalizeOnDiscovery(t *testing.T) {
	root := t.TempDir()
	engine := testExtensionManifest("old-engine")
	engine.Kind = "driver"
	engine.Driver = "old-engine"
	writeTestModuleManifest(t, filepath.Join(root, engine.Name), engine)
	transport := testExtensionManifest("old-transport")
	transport.Kind = "extension"
	writeTestModuleManifest(t, filepath.Join(root, transport.Name), transport)

	modules, err := DiscoverModules(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 || modules[0].Manifest.Kind != ModuleKindEngine || modules[1].Manifest.Kind != ModuleKindTransport {
		t.Fatalf("legacy kinds were not normalized: %#v", modules)
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
	system := filepath.Join(t.TempDir(), "system")
	t.Setenv("KVLITE_MODULE_PATH", first+string(os.PathListSeparator)+second+string(os.PathListSeparator)+first)
	t.Setenv("KVLITE_HOME", home)
	t.Setenv("KVLITE_SYSTEM_MODULE_PATH", system)

	paths := DefaultModulePaths()
	want := []string{
		filepath.Clean(first),
		filepath.Clean(second),
		filepath.Join(home, "modules"),
		filepath.Join(home, "drivers"),
		filepath.Clean(system),
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
		wantKind := ModuleKindEngine
		if module.Manifest.Name == "http" || module.Manifest.Name == "redis" {
			wantKind = ModuleKindTransport
		}
		if module.Manifest.Kind != wantKind {
			t.Errorf("module %q kind = %q, want %q", module.Manifest.Name, module.Manifest.Kind, wantKind)
		}
	}
	want := []string{"badgerdb", "berkeleydb", "boltdb", "http", "leveldb", "lmdb", "redis", "rocksdb"}
	if !slices.Equal(got, want) {
		t.Fatalf("source module names = %#v, want %#v", got, want)
	}
}

func TestGroupedCatalogRootDiscoversDriversAndModules(t *testing.T) {
	root := t.TempDir()
	engine := testExtensionManifest("leveldb")
	engine.Kind = ModuleKindEngine
	engine.Driver = DriverLevelDB
	writeTestModuleManifest(t, filepath.Join(root, "drivers", "leveldb"), engine)
	writeTestModuleManifest(t, filepath.Join(root, "modules", "http"), testExtensionManifest("http"))
	modules, err := DiscoverModules(root)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(modules))
	for _, module := range modules {
		got = append(got, module.Manifest.Name)
	}
	if !slices.Equal(got, []string{"http", "leveldb"}) {
		t.Fatalf("grouped catalog names = %#v, want [http leveldb]", got)
	}
}

func testExtensionManifest(name string) ModuleManifest {
	return ModuleManifest{
		SchemaVersion: ModuleManifestVersion,
		Name:          name,
		Kind:          ModuleKindTransport,
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
