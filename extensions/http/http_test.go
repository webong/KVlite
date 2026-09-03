package kvlitehttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/webong/kvlite"
)

const (
	testPrimaryDriver   kvlite.DriverName = "http-test-primary"
	testSecondaryDriver kvlite.DriverName = "http-test-secondary"
)

var testEngines = struct {
	sync.Mutex
	items map[string]*testEngine
}{items: make(map[string]*testEngine)}

type testDriver struct {
	name kvlite.DriverName
}

func init() {
	kvlite.MustRegisterDriver(testDriver{name: testPrimaryDriver})
	kvlite.MustRegisterDriver(testDriver{name: testSecondaryDriver})
}

func (driver testDriver) Info() kvlite.DriverInfo {
	return kvlite.DriverInfo{
		Driver:         driver.name,
		Implementation: "kvlite-http-test",
		Format:         "v1",
		Version:        "test",
	}
}

func (testDriver) Available() error { return nil }

func (driver testDriver) Open(path string, _ kvlite.DriverOptions) (kvlite.Engine, error) {
	testEngines.Lock()
	defer testEngines.Unlock()
	engine := testEngines.items[path]
	if engine == nil {
		engine = &testEngine{values: make(map[string][]byte)}
		testEngines.items[path] = engine
	}
	return engine, nil
}

type testEngine struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func (engine *testEngine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	value, found := engine.values[string(key)]
	return append([]byte(nil), value...), found, nil
}

func (engine *testEngine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.values[string(key)] = append([]byte(nil), value...)
	return nil
}

func (engine *testEngine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	delete(engine.values, string(key))
	return nil
}

func (engine *testEngine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	engine.mu.RLock()
	keys := make([]string, 0)
	for key := range engine.values {
		if bytes.HasPrefix([]byte(key), prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	items := make([][2][]byte, 0, len(keys))
	for _, key := range keys {
		items = append(items, [2][]byte{[]byte(key), append([]byte(nil), engine.values[key]...)})
	}
	engine.mu.RUnlock()
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := callback(item[0], item[1]); err != nil {
			return err
		}
	}
	return nil
}

func (engine *testEngine) Close() error { return nil }

func openTestOwner(t *testing.T, serverOptions Options) (*kvlite.DB, *Server) {
	t.Helper()
	owner, err := kvlite.Open(t.TempDir(), kvlite.WithDriver(string(testPrimaryDriver)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	server, err := Serve(owner, serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return owner, server
}

func TestServeAndConnectAreExplicitExtensions(t *testing.T) {
	secondaryPath := t.TempDir()
	const redisURL = "redis://127.0.0.1:6379"
	owner, server := openTestOwner(t, Options{
		ListenAddress: "127.0.0.1:0",
		BearerToken:   "secret",
		RedisURL:      redisURL,
		DriverPaths: map[kvlite.DriverName]string{
			testSecondaryDriver: secondaryPath,
		},
	})

	remote, err := Connect(server.URL(), ClientOptions{
		BearerToken: "secret",
		Driver:      testSecondaryDriver,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })

	ctx := context.Background()
	if err := remote.Put(ctx, "selected", "secondary"); err != nil {
		t.Fatal(err)
	}
	secondary := server.databases.databases[testSecondaryDriver]
	var value string
	if err := secondary.Get(ctx, "selected", &value); err != nil || value != "secondary" {
		t.Fatalf("secondary database value/error = %q, %v", value, err)
	}
	if err := owner.Get(ctx, "selected", &value); !errors.Is(err, kvlite.ErrNotFound) {
		t.Fatalf("primary database unexpectedly received remote value: %v", err)
	}

	if length, err := remote.RPush(ctx, "jobs", "one", "two"); err != nil || length != 2 {
		t.Fatalf("remote RPush() = %d, %v", length, err)
	}
	var jobs []string
	if err := secondary.LRange(ctx, "jobs", 0, -1, &jobs); err != nil || !equalStrings(jobs, []string{"one", "two"}) {
		t.Fatalf("secondary LRange() = %#v, %v", jobs, err)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL()+"/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set(driverHeader, string(testSecondaryDriver))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1 status = %s", response.Status)
	}
	var metadata struct {
		Driver  kvlite.DriverName   `json:"driver"`
		Drivers []kvlite.DriverInfo `json:"drivers"`
		Redis   string              `json:"redis"`
	}
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Driver != testSecondaryDriver || len(metadata.Drivers) != 2 || metadata.Redis != "" {
		t.Fatalf("unexpected discovery metadata: %#v", metadata)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL()+"/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var primaryMetadata struct {
		Redis string `json:"redis"`
	}
	if err := json.NewDecoder(response.Body).Decode(&primaryMetadata); err != nil {
		t.Fatal(err)
	}
	if primaryMetadata.Redis != redisURL {
		t.Fatalf("primary Redis URL = %q, want %q", primaryMetadata.Redis, redisURL)
	}
}

func TestJSONAPIAndAuthentication(t *testing.T) {
	owner, server := openTestOwner(t, Options{
		ListenAddress: "127.0.0.1:0",
		BearerToken:   "secret",
	})
	key := base64.RawURLEncoding.EncodeToString([]byte("http-key"))
	request, err := http.NewRequest(http.MethodPut, server.URL()+"/v1/entries/"+key+"?ttl_seconds=60", bytes.NewBufferString(`{"answer":42}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %s", response.Status)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL()+"/v1/entries/"+key, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != `{"answer":42}` {
		t.Fatalf("GET status/body = %s/%s", response.Status, body)
	}

	var stored map[string]int
	if err := owner.Get(context.Background(), "http-key", &stored); err != nil || stored["answer"] != 42 {
		t.Fatalf("owner value/error = %#v, %v", stored, err)
	}

	response, err = http.Get(server.URL() + "/v1/entries/" + key)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized GET status = %s", response.Status)
	}
}

func TestConnectReportsDriverErrorsImmediately(t *testing.T) {
	_, server := openTestOwner(t, Options{ListenAddress: "127.0.0.1:0"})
	_, err := Connect(server.URL(), ClientOptions{Driver: testSecondaryDriver})
	if !errors.Is(err, kvlite.ErrDriverNotExposed) {
		t.Fatalf("Connect() error = %v, want ErrDriverNotExposed", err)
	}
	var routeError *RemoteDriverError
	if !errors.As(err, &routeError) || routeError.Code != "driver_not_exposed" {
		t.Fatalf("remote route error = %#v", err)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL()+"/v1/health", nil)
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
	_, err = Connect(server.URL(), ClientOptions{Driver: kvlite.DriverBerkeleyDB})
	if !errors.Is(err, kvlite.ErrDriverNotInstalled) {
		t.Fatalf("Connect() error = %v, want ErrDriverNotInstalled", err)
	}
}

func TestClosingServerLeavesEmbeddedOwnerOpen(t *testing.T) {
	owner, server := openTestOwner(t, Options{ListenAddress: "127.0.0.1:0"})
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Put(context.Background(), "embedded", "still-open"); err != nil {
		t.Fatalf("owner Put() after Server.Close() = %v", err)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
