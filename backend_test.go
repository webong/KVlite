package kvlite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const (
	testMemoryDriverName DriverName = "test-memory"
	testOtherDriverName  DriverName = "test-other"
)

var testDriverEngines = struct {
	sync.Mutex
	items map[string]*memoryEngine
}{items: make(map[string]*memoryEngine)}

type testRegisteredDriver struct {
	name           DriverName
	implementation string
}

func init() {
	MustRegisterDriver(testRegisteredDriver{name: testMemoryDriverName, implementation: "kvlite-test-memory"})
	MustRegisterDriver(testRegisteredDriver{name: testOtherDriverName, implementation: "kvlite-test-other"})
}

func (driver testRegisteredDriver) Info() DriverInfo {
	return DriverInfo{
		Driver:         driver.name,
		Implementation: driver.implementation,
		Format:         "v1",
		Version:        "test",
	}
}

func (testRegisteredDriver) Available() error { return nil }

func (driver testRegisteredDriver) Open(path string, _ DriverOptions) (Engine, error) {
	testDriverEngines.Lock()
	defer testDriverEngines.Unlock()
	storage := testDriverEngines.items[path]
	if storage == nil {
		storage = newMemoryEngine()
		testDriverEngines.items[path] = storage
	}
	return storage, nil
}

func TestOpenRegisteredDriverPersistsDataAndWritesManifest(t *testing.T) {
	path := t.TempDir()
	ctx := context.Background()

	db, err := Open(path, WithDriver(string(testMemoryDriverName)))
	if err != nil {
		t.Fatal(err)
	}
	if got := db.Backend(); got != testMemoryDriverName {
		t.Fatalf("Backend() = %q, want %q", got, testMemoryDriverName)
	}
	if err := db.Put(ctx, "user:101", map[string]any{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(path, backendManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("backend manifest is empty")
	}
	manifest, found, err := readBackendManifest(path)
	if err != nil || !found {
		t.Fatalf("readBackendManifest() = %#v, %v, %v", manifest, found, err)
	}
	if manifest.Backend != testMemoryDriverName || manifest.Driver != "kvlite-test-memory" || manifest.DriverFormat != "v1" || manifest.DriverVersion != "test" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}

	reopened, err := Open(path, WithDriver(string(testMemoryDriverName)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var user map[string]any
	if err := reopened.Get(ctx, "user:101", &user); err != nil {
		t.Fatal(err)
	}
	if user["name"] != "Ada" {
		t.Fatalf("reopened value = %#v", user)
	}
}

func TestBackendManifestRejectsDifferentDriver(t *testing.T) {
	path := t.TempDir()
	db, err := Open(path, WithDriver(string(testMemoryDriverName)))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, otherDriver, err := registeredDriverFor(testOtherDriverName)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepareBackendManifest(path, otherDriver, false)
	if !errors.Is(err, ErrBackendMismatch) {
		t.Fatalf("prepareBackendManifest() error = %v, want ErrBackendMismatch", err)
	}
}

func TestSharedServerRoutesExplicitRemoteDriver(t *testing.T) {
	primaryPath := filepath.Join(t.TempDir(), "primary")
	secondaryPath := filepath.Join(t.TempDir(), "secondary")
	owner, err := Open(primaryPath,
		WithDriver(string(testMemoryDriverName)),
		WithSharing(SharingOptions{
			ListenAddress: "127.0.0.1:0",
			DriverPaths: map[DriverName]string{
				testOtherDriverName: secondaryPath,
			},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })

	remote, err := OpenRemote(owner.SharingAddress(), RemoteOptions{}, WithDriver(string(testOtherDriverName)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	ctx := context.Background()
	if err := remote.Put(ctx, "selected", "secondary"); err != nil {
		t.Fatal(err)
	}

	secondary := owner.server.databases.databases[testOtherDriverName]
	var value string
	if err := secondary.Get(ctx, "selected", &value); err != nil || value != "secondary" {
		t.Fatalf("secondary database value/error = %q, %v", value, err)
	}
	if err := owner.Get(ctx, "selected", &value); !errors.Is(err, ErrNotFound) {
		t.Fatalf("primary database unexpectedly received remote value: %v", err)
	}

	request, err := http.NewRequest(http.MethodGet, owner.SharingAddress()+"/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(driverHeader, string(testOtherDriverName))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1 status = %s", response.Status)
	}
	var metadata struct {
		Driver  DriverName   `json:"driver"`
		Drivers []DriverInfo `json:"drivers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Driver != testOtherDriverName || len(metadata.Drivers) != 2 {
		t.Fatalf("unexpected discovery metadata: %#v", metadata)
	}
}

func TestSharedServerRejectsMissingRemoteDriverClearly(t *testing.T) {
	owner, err := Open(t.TempDir(),
		WithDriver(string(testMemoryDriverName)),
		WithSharing(SharingOptions{ListenAddress: "127.0.0.1:0"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })

	_, err = OpenRemote(owner.SharingAddress(), RemoteOptions{}, WithDriver(string(testOtherDriverName)))
	if !errors.Is(err, ErrDriverNotExposed) {
		t.Fatalf("OpenRemote() error = %v, want ErrDriverNotExposed", err)
	}
	var notExposed *RemoteDriverError
	if !errors.As(err, &notExposed) || notExposed.Code != "driver_not_exposed" {
		t.Fatalf("unmapped remote driver error = %#v", err)
	}

	request, err := http.NewRequest(http.MethodGet, owner.SharingAddress()+"/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(driverHeader, "berkeleydb")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotImplemented {
		t.Fatalf("missing driver response = %s, want 501", response.Status)
	}
	var payload struct {
		Error struct {
			Code   string     `json:"code"`
			Driver DriverName `json:"driver"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "driver_not_installed" || payload.Error.Driver != DriverBerkeleyDB {
		t.Fatalf("missing driver payload = %#v", payload)
	}

	_, err = OpenRemote(owner.SharingAddress(), RemoteOptions{}, WithDriver("berkeleydb"))
	if !errors.Is(err, ErrDriverNotInstalled) {
		t.Fatalf("OpenRemote() error = %v, want ErrDriverNotInstalled", err)
	}
	var remoteDriverError *RemoteDriverError
	if !errors.As(err, &remoteDriverError) || remoteDriverError.Code != "driver_not_installed" {
		t.Fatalf("remote driver error = %#v", err)
	}
}

func TestOpenRejectsUnavailableBerkeleyDBWithoutTouchingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "berkeley")
	_, err := Open(path, WithDriver("berkeleydb"))
	if !errors.Is(err, ErrDriverNotInstalled) {
		t.Fatalf("Open() error = %v, want ErrDriverNotInstalled", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Open() created unavailable backend path: stat error = %v", statErr)
	}
}

func TestDriversReportOnlyInstalledDrivers(t *testing.T) {
	info, err := DriverInfoFor(testMemoryDriverName)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.Driver != testMemoryDriverName {
		t.Fatalf("test driver info = %#v", info)
	}
	_, err = DriverInfoFor(DriverBerkeleyDB)
	if !errors.Is(err, ErrDriverNotInstalled) {
		t.Fatalf("DriverInfoFor(berkeleydb) error = %v, want ErrDriverNotInstalled", err)
	}
}
