package kvlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	// ModuleManifestFilename is the fixed, explicit filename recognised by the
	// KVLite module catalog. The resolver deliberately never scans arbitrary
	// shared libraries from the current directory.
	ModuleManifestFilename = "kvlite-module.json"
	// ModuleManifestVersion describes the JSON manifest schema.
	ModuleManifestVersion = 1
	// ModuleABIVersion is the compatibility number shared by a host and an
	// independently distributed module. It is distinct from the C embedding ABI
	// exposed by capi/kvlite.h.
	ModuleABIVersion = 1
)

// ModuleKind identifies a KVLite module category.
type ModuleKind string

const (
	// ModuleKindDriver contains an embedded storage-engine implementation.
	ModuleKindDriver ModuleKind = "driver"
	// ModuleKindExtension contains an optional capability such as HTTP or
	// Redis. KVLite core remains embeddable without one.
	ModuleKindExtension ModuleKind = "extension"
)

// ModuleArtifactKind identifies a packaged module artifact. The current
// release builder emits c-shared and executable artifacts. Native modules are
// reserved for the future stable C module loader; Go's runtime plugin system
// is intentionally not part of this contract.
type ModuleArtifactKind string

const (
	ModuleArtifactCShared    ModuleArtifactKind = "c-shared"
	ModuleArtifactExecutable ModuleArtifactKind = "executable"
	ModuleArtifactNative     ModuleArtifactKind = "native-module"
)

// ModuleDependency declares one other named module required by a module. A
// zero ModuleABI accepts the host's current module ABI; a non-zero value must
// match exactly.
type ModuleDependency struct {
	Name      string `json:"name"`
	ModuleABI int    `json:"module_abi,omitempty"`
}

// ModuleArtifact is one platform-specific executable or library described by
// a module manifest. Path is always relative to the manifest directory.
type ModuleArtifact struct {
	Platform string             `json:"platform"`
	Kind     ModuleArtifactKind `json:"kind"`
	Path     string             `json:"path"`
	SHA256   string             `json:"sha256,omitempty"`
	// Symbol identifies the exported C symbol used for native-module loading.
	// It is descriptive for a c-shared artifact, whose public entry points are
	// defined by capi/kvlite.h.
	Symbol string `json:"symbol,omitempty"`
}

// ModuleManifest is the portable, inspectable description of one optional
// KVLite capability. It describes an artifact; discovery does not execute it.
// A caller must explicitly choose a driver or enable an extension afterwards.
type ModuleManifest struct {
	SchemaVersion int        `json:"schema_version"`
	Name          string     `json:"name"`
	Kind          ModuleKind `json:"kind"`
	Version       string     `json:"version"`
	ModuleABI     int        `json:"module_abi"`
	// Driver is required only for a storage driver and is the name accepted by
	// WithDriver. Extensions leave it empty.
	Driver       DriverName         `json:"driver,omitempty"`
	Capabilities []string           `json:"capabilities,omitempty"`
	Dependencies []ModuleDependency `json:"dependencies,omitempty"`
	Artifacts    []ModuleArtifact   `json:"artifacts,omitempty"`
	License      string             `json:"license"`
}

// Module is a manifest paired with the location from which it was discovered.
// Linked is true only for an in-process Go module that registered itself; it
// does not imply that an on-disk module artifact has been dynamically loaded.
type Module struct {
	Manifest     ModuleManifest
	Directory    string
	ManifestPath string
	Linked       bool
}

var linkedModuleRegistry = struct {
	sync.RWMutex
	modules map[string]ModuleManifest
}{modules: make(map[string]ModuleManifest)}

// RegisterLinkedModule registers metadata for a Go module already linked into
// the current process. It keeps the existing normal-Go-import experience while
// using the same manifest shape as packaged artifacts.
func RegisterLinkedModule(manifest ModuleManifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	linkedModuleRegistry.Lock()
	defer linkedModuleRegistry.Unlock()
	if _, exists := linkedModuleRegistry.modules[manifest.Name]; exists {
		return fmt.Errorf("%w: %q", ErrModuleConflict, manifest.Name)
	}
	linkedModuleRegistry.modules[manifest.Name] = cloneModuleManifest(manifest)
	return nil
}

// MustRegisterLinkedModule is RegisterLinkedModule for a module's init
// function. Invalid linked module metadata is a build-time programming error.
func MustRegisterLinkedModule(manifest ModuleManifest) {
	if err := RegisterLinkedModule(manifest); err != nil {
		panic(err)
	}
}

// LinkedModules returns metadata for modules linked into this process.
func LinkedModules() []Module {
	linkedModuleRegistry.RLock()
	modules := make([]Module, 0, len(linkedModuleRegistry.modules))
	for _, manifest := range linkedModuleRegistry.modules {
		modules = append(modules, Module{Manifest: cloneModuleManifest(manifest), Linked: true})
	}
	linkedModuleRegistry.RUnlock()
	sort.Slice(modules, func(left, right int) bool {
		return modules[left].Manifest.Name < modules[right].Manifest.Name
	})
	return modules
}

// LinkedModule returns metadata for one Go module linked into this process.
func LinkedModule(name string) (Module, error) {
	canonical, err := normalizeModuleName(name)
	if err != nil {
		return Module{}, err
	}
	linkedModuleRegistry.RLock()
	manifest, found := linkedModuleRegistry.modules[canonical]
	linkedModuleRegistry.RUnlock()
	if !found {
		return Module{}, fmt.Errorf("%w: %q", ErrModuleNotInstalled, canonical)
	}
	return Module{Manifest: cloneModuleManifest(manifest), Linked: true}, nil
}

// DefaultModulePaths returns the only directories searched when no explicit
// paths are supplied. KVLITE_MODULE_PATH is a platform path-list of module
// roots. KVLITE_HOME/modules is preferred; KVLITE_HOME/drivers is retained as
// a compatibility location for the existing driver-bundle layout.
// KVLITE_SYSTEM_MODULE_PATH is a platform path-list of read-only system roots
// installed by a package manager (for example /usr/local/lib/kvlite); it is
// searched last so a user installation always shadows the system one.
//
// The current working directory and arbitrary library search paths are never
// searched implicitly. Applications should supply an explicit module path when
// they need a private installation location.
func DefaultModulePaths() []string {
	paths := make([]string, 0)
	if configured := os.Getenv("KVLITE_MODULE_PATH"); configured != "" {
		paths = append(paths, filepath.SplitList(configured)...)
	}
	if home := strings.TrimSpace(os.Getenv("KVLITE_HOME")); home != "" {
		paths = append(paths,
			filepath.Join(home, "modules"),
			filepath.Join(home, "drivers"),
		)
	}
	if system := os.Getenv("KVLITE_SYSTEM_MODULE_PATH"); system != "" {
		paths = append(paths, filepath.SplitList(system)...)
	}
	return uniqueModulePaths(paths)
}

// DiscoverModules reads module descriptors from direct children of each root,
// or from a root that is itself a module directory. It validates descriptors
// but never loads an executable or native library.
func DiscoverModules(paths ...string) ([]Module, error) {
	if len(paths) == 0 {
		paths = DefaultModulePaths()
	}
	modules := make(map[string]Module)
	for _, root := range uniqueModulePaths(paths) {
		items, err := discoverModulesAt(root)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if previous, exists := modules[item.Manifest.Name]; exists {
				return nil, fmt.Errorf(
					"%w: module %q appears in both %q and %q",
					ErrModuleConflict,
					item.Manifest.Name,
					previous.ManifestPath,
					item.ManifestPath,
				)
			}
			modules[item.Manifest.Name] = item
		}
	}
	result := make([]Module, 0, len(modules))
	for _, item := range modules {
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Manifest.Name < result[right].Manifest.Name
	})
	return result, nil
}

// FindInstalledModule resolves one on-disk module by its stable name. It does
// not fall back to a linked Go package, so callers can distinguish artifact
// discovery from compile-time registration.
func FindInstalledModule(name string, paths ...string) (Module, error) {
	canonical, err := normalizeModuleName(name)
	if err != nil {
		return Module{}, err
	}
	modules, err := DiscoverModules(paths...)
	if err != nil {
		return Module{}, err
	}
	for _, item := range modules {
		if item.Manifest.Name == canonical {
			return item, nil
		}
	}
	return Module{}, fmt.Errorf("%w: %q", ErrModuleNotInstalled, canonical)
}

// ResolveModule prefers an explicitly installed module artifact and falls back
// to metadata for a linked Go module. It only resolves metadata; hosts must
// still enforce their own load policy and activate a module explicitly.
func ResolveModule(name string, paths ...string) (Module, error) {
	installed, err := FindInstalledModule(name, paths...)
	if err == nil {
		return installed, nil
	}
	if !errors.Is(err, ErrModuleNotInstalled) {
		return Module{}, err
	}
	return LinkedModule(name)
}

// ResolveModuleExecutable resolves an installed module that exposes an executable
// artifact for the current platform. Linked modules are not eligible because they
// already exist in-process and cannot be re-executed.
func ResolveModuleExecutable(name string, paths ...string) (Module, ModuleArtifact, error) {
	module, err := ResolveModule(name, paths...)
	if err != nil {
		return Module{}, ModuleArtifact{}, err
	}
	if module.Linked {
		return Module{}, ModuleArtifact{}, fmt.Errorf("%w: module %q is linked into this process", ErrModuleNoExecutable, module.Manifest.Name)
	}
	artifact, err := module.ArtifactForCurrentPlatform(ModuleArtifactExecutable)
	if err != nil {
		return Module{}, ModuleArtifact{}, fmt.Errorf("%w: module %q", ErrModuleNoExecutable, module.Manifest.Name)
	}
	if err := module.Verify(); err != nil {
		return Module{}, ModuleArtifact{}, err
	}
	return module, artifact, nil
}

// ArtifactForCurrentPlatform returns the matching packaged artifact. Optional
// kinds limit the acceptable artifact kinds; when omitted, any artifact type
// is considered.
func (module Module) ArtifactForCurrentPlatform(kinds ...ModuleArtifactKind) (ModuleArtifact, error) {
	target := runtime.GOOS + "-" + runtime.GOARCH
	for _, artifact := range module.Manifest.Artifacts {
		if artifact.Platform != target || !moduleArtifactKindAllowed(artifact.Kind, kinds) {
			continue
		}
		return artifact, nil
	}
	return ModuleArtifact{}, fmt.Errorf(
		"%w: module %q has no artifact for %s",
		ErrModuleArtifactMissing,
		module.Manifest.Name,
		target,
	)
}

// ArtifactPath returns the safe absolute path of an artifact relative to the
// module manifest. It is useful to a host after it has chosen to load or start
// a specific module artifact.
func (module Module) ArtifactPath(artifact ModuleArtifact) (string, error) {
	if module.Directory == "" {
		return "", fmt.Errorf("%w: linked module %q has no on-disk artifact", ErrModuleArtifactMissing, module.Manifest.Name)
	}
	if err := validateModuleArtifact(artifact); err != nil {
		return "", err
	}
	return filepath.Join(module.Directory, filepath.FromSlash(artifact.Path)), nil
}

// Verify checks that every artifact named by an installed module exists and
// matches its required SHA-256 digest. It does not execute the artifact or
// infer trust from its directory name.
func (module Module) Verify() error {
	if module.Linked {
		return nil
	}
	if len(module.Manifest.Artifacts) == 0 {
		return fmt.Errorf("%w: module %q declares no packaged artifacts", ErrModuleArtifactMissing, module.Manifest.Name)
	}
	for _, artifact := range module.Manifest.Artifacts {
		artifactPath, err := module.ArtifactPath(artifact)
		if err != nil {
			return err
		}
		file, err := os.Open(artifactPath)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %q", ErrModuleArtifactMissing, artifactPath)
		}
		if err != nil {
			return fmt.Errorf("kvlite: read module artifact %q: %w", artifactPath, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("kvlite: hash module artifact %q: %w", artifactPath, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("kvlite: close module artifact %q: %w", artifactPath, closeErr)
		}
		if artifact.SHA256 == "" {
			return fmt.Errorf("%w: %q has no SHA-256 checksum", ErrModuleIntegrity, artifactPath)
		}
		if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
			return fmt.Errorf("%w: %q", ErrModuleIntegrity, artifactPath)
		}
	}
	return nil
}

// Validate verifies that a manifest is safe and compatible before a host uses
// it for discovery. It intentionally does not check whether artifacts exist;
// use Module.Verify after discovery for that operation.
func (manifest ModuleManifest) Validate() error {
	if manifest.SchemaVersion != ModuleManifestVersion {
		return fmt.Errorf("%w: module %q uses schema version %d, expected %d", ErrModuleManifestInvalid, manifest.Name, manifest.SchemaVersion, ModuleManifestVersion)
	}
	canonicalName, err := normalizeModuleName(manifest.Name)
	if err != nil {
		return err
	}
	if manifest.Name != canonicalName {
		return fmt.Errorf("%w: module name %q must be canonical %q", ErrModuleManifestInvalid, manifest.Name, canonicalName)
	}
	if manifest.Kind != ModuleKindDriver && manifest.Kind != ModuleKindExtension {
		return fmt.Errorf("%w: module %q has unsupported kind %q", ErrModuleManifestInvalid, manifest.Name, manifest.Kind)
	}
	if strings.TrimSpace(manifest.Version) == "" || strings.IndexFunc(manifest.Version, unicodeWhitespace) >= 0 {
		return fmt.Errorf("%w: module %q must provide a non-whitespace version", ErrModuleManifestInvalid, manifest.Name)
	}
	if manifest.ModuleABI != ModuleABIVersion {
		return fmt.Errorf("%w: module %q requires module ABI %d, host supports %d", ErrModuleIncompatible, manifest.Name, manifest.ModuleABI, ModuleABIVersion)
	}
	if strings.TrimSpace(manifest.License) == "" {
		return fmt.Errorf("%w: module %q must declare a license", ErrModuleManifestInvalid, manifest.Name)
	}
	if manifest.Kind == ModuleKindDriver {
		canonicalDriver, err := ParseDriverName(string(manifest.Driver))
		if err != nil {
			return fmt.Errorf("%w: module %q: %v", ErrModuleManifestInvalid, manifest.Name, err)
		}
		if manifest.Driver != canonicalDriver {
			return fmt.Errorf("%w: module %q driver %q must be canonical %q", ErrModuleManifestInvalid, manifest.Name, manifest.Driver, canonicalDriver)
		}
	} else if manifest.Driver != "" {
		return fmt.Errorf("%w: extension module %q cannot select driver %q", ErrModuleManifestInvalid, manifest.Name, manifest.Driver)
	}
	if err := validateModuleTokens(manifest.Name, "capability", manifest.Capabilities); err != nil {
		return err
	}
	dependencies := make(map[string]struct{}, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		canonical, err := normalizeModuleName(dependency.Name)
		if err != nil || dependency.Name != canonical {
			return fmt.Errorf("%w: module %q has invalid dependency %q", ErrModuleManifestInvalid, manifest.Name, dependency.Name)
		}
		if dependency.Name == manifest.Name {
			return fmt.Errorf("%w: module %q cannot depend on itself", ErrModuleManifestInvalid, manifest.Name)
		}
		if dependency.ModuleABI != 0 && dependency.ModuleABI != ModuleABIVersion {
			return fmt.Errorf("%w: module %q dependency %q requires module ABI %d", ErrModuleIncompatible, manifest.Name, dependency.Name, dependency.ModuleABI)
		}
		if _, exists := dependencies[dependency.Name]; exists {
			return fmt.Errorf("%w: module %q declares dependency %q more than once", ErrModuleManifestInvalid, manifest.Name, dependency.Name)
		}
		dependencies[dependency.Name] = struct{}{}
	}
	artifacts := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if err := validateModuleArtifact(artifact); err != nil {
			return fmt.Errorf("%w: module %q: %v", ErrModuleManifestInvalid, manifest.Name, err)
		}
		// A host selects an artifact by platform and kind. Permit an executable
		// and a C shared library for one platform, but never make that selection
		// ambiguous with two artifacts of the same kind.
		key := artifact.Platform + "\x00" + string(artifact.Kind)
		if _, exists := artifacts[key]; exists {
			return fmt.Errorf("%w: module %q lists more than one %s artifact for %s", ErrModuleManifestInvalid, manifest.Name, artifact.Kind, artifact.Platform)
		}
		artifacts[key] = struct{}{}
	}
	return nil
}

func discoverModulesAt(root string) ([]Module, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kvlite: inspect module path %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: module path %q is not a directory", ErrModuleManifestInvalid, root)
	}
	if module, found, err := readModuleAt(root); err != nil {
		return nil, err
	} else if found {
		return []Module{module}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("kvlite: read module path %q: %w", root, err)
	}
	modules := make([]Module, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(root, entry.Name())
		module, found, err := readModuleAt(child)
		if err != nil {
			return nil, err
		}
		if found {
			modules = append(modules, module)
			continue
		}
		// Release trees group bundles under drivers/ and modules/
		// subdirectories (see scripts/install.sh). Descend exactly one
		// level into those conventional grouping directories so a catalog
		// root stays usable as a single search path.
		if entry.Name() != "drivers" && entry.Name() != "modules" {
			continue
		}
		nested, err := os.ReadDir(child)
		if err != nil {
			return nil, fmt.Errorf("kvlite: read module path %q: %w", child, err)
		}
		for _, nestedEntry := range nested {
			if !nestedEntry.IsDir() {
				continue
			}
			module, found, err := readModuleAt(filepath.Join(child, nestedEntry.Name()))
			if err != nil {
				return nil, err
			}
			if found {
				modules = append(modules, module)
			}
		}
	}
	return modules, nil
}

func readModuleAt(directory string) (Module, bool, error) {
	manifestPath := filepath.Join(directory, ModuleManifestFilename)
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return Module{}, false, nil
	}
	if err != nil {
		return Module{}, false, fmt.Errorf("kvlite: read module manifest %q: %w", manifestPath, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest ModuleManifest
	if err := decoder.Decode(&manifest); err != nil {
		return Module{}, false, fmt.Errorf("%w: %q: %v", ErrModuleManifestInvalid, manifestPath, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Module{}, false, fmt.Errorf("%w: %q contains multiple JSON values", ErrModuleManifestInvalid, manifestPath)
	}
	if err := manifest.Validate(); err != nil {
		return Module{}, false, fmt.Errorf("%w: %q: %w", ErrModuleManifestInvalid, manifestPath, err)
	}
	return Module{
		Manifest:     cloneModuleManifest(manifest),
		Directory:    directory,
		ManifestPath: manifestPath,
	}, true, nil
}

func normalizeModuleName(name string) (string, error) {
	canonical := strings.ToLower(strings.TrimSpace(name))
	if canonical == "" {
		return "", fmt.Errorf("%w: module name is required", ErrModuleManifestInvalid)
	}
	for index, character := range canonical {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return "", fmt.Errorf("%w: module name %q contains an unsupported character at byte %d", ErrModuleManifestInvalid, name, index)
	}
	return canonical, nil
}

func validateModuleTokens(moduleName, label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		canonical, err := normalizeModuleName(value)
		if err != nil || value != canonical {
			return fmt.Errorf("%w: module %q has invalid %s %q", ErrModuleManifestInvalid, moduleName, label, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: module %q declares %s %q more than once", ErrModuleManifestInvalid, moduleName, label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateModuleArtifact(artifact ModuleArtifact) error {
	if !validModulePlatform(artifact.Platform) {
		return fmt.Errorf("artifact platform %q is invalid", artifact.Platform)
	}
	if artifact.Kind != ModuleArtifactCShared && artifact.Kind != ModuleArtifactExecutable && artifact.Kind != ModuleArtifactNative {
		return fmt.Errorf("artifact kind %q is unsupported", artifact.Kind)
	}
	if !safeModuleRelativePath(artifact.Path) {
		return fmt.Errorf("artifact path %q must be a relative slash-separated path", artifact.Path)
	}
	if artifact.SHA256 != "" {
		if len(artifact.SHA256) != sha256.Size*2 {
			return fmt.Errorf("artifact %q has an invalid SHA-256 checksum", artifact.Path)
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return fmt.Errorf("artifact %q has an invalid SHA-256 checksum", artifact.Path)
		}
	}
	if artifact.Kind == ModuleArtifactNative && strings.TrimSpace(artifact.Symbol) == "" {
		return fmt.Errorf("native module artifact %q must name its initialization symbol", artifact.Path)
	}
	return nil
}

func validModulePlatform(platform string) bool {
	if platform == "" || platform != strings.ToLower(platform) || strings.Count(platform, "-") != 1 {
		return false
	}
	for _, character := range platform {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return false
	}
	return true
}

func safeModuleRelativePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && clean == value
}

func moduleArtifactKindAllowed(kind ModuleArtifactKind, wanted []ModuleArtifactKind) bool {
	if len(wanted) == 0 {
		return true
	}
	for _, candidate := range wanted {
		if kind == candidate {
			return true
		}
	}
	return false
}

func uniqueModulePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		candidate = filepath.Clean(candidate)
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func cloneModuleManifest(manifest ModuleManifest) ModuleManifest {
	result := manifest
	result.Capabilities = append([]string(nil), manifest.Capabilities...)
	result.Dependencies = append([]ModuleDependency(nil), manifest.Dependencies...)
	result.Artifacts = append([]ModuleArtifact(nil), manifest.Artifacts...)
	return result
}

func unicodeWhitespace(character rune) bool {
	return character == ' ' || character == '\t' || character == '\n' || character == '\r' || character == '\v' || character == '\f'
}
