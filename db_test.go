package kvlite

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
	"time"
)

type memoryEngine struct {
	mu     sync.RWMutex
	data   map[string][]byte
	closed bool
}

func newMemoryEngine() *memoryEngine {
	return &memoryEngine{data: make(map[string][]byte)}
}

func (engine *memoryEngine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	value, found := engine.data[string(key)]
	return append([]byte(nil), value...), found, nil
}

func (engine *memoryEngine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.data[string(key)] = append([]byte(nil), value...)
	return nil
}

func (engine *memoryEngine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	delete(engine.data, string(key))
	return nil
}

func (engine *memoryEngine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	engine.mu.RLock()
	keys := make([]string, 0)
	for key := range engine.data {
		if bytes.HasPrefix([]byte(key), prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	items := make([][2][]byte, 0, len(keys))
	for _, key := range keys {
		items = append(items, [2][]byte{[]byte(key), append([]byte(nil), engine.data[key]...)})
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

func (engine *memoryEngine) Close() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.closed = true
	return nil
}

func testDB(t *testing.T, options ...Option) (*DB, *memoryEngine) {
	t.Helper()
	cfg, err := buildConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	storage := newMemoryEngine()
	db, err := newDB(storage, cfg, BackendRocksDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, storage
}

func TestPutGetAndGetAs(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	type user struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	want := user{ID: 101, Name: "Ada"}
	if err := db.Put(ctx, "user:101", want); err != nil {
		t.Fatal(err)
	}
	got, err := GetAs[user](ctx, db, "user:101")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	exists, err := db.Has(ctx, "user:101")
	if err != nil || !exists {
		t.Fatalf("Has() = %v, %v", exists, err)
	}
}

func TestPutBytesPreservesSerializedPayload(t *testing.T) {
	db, _ := testDB(t)
	want := []byte{0, 1, 2, 255}
	if err := db.PutBytes(context.Background(), "binary", want); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetBytes(context.Background(), "binary")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("GetBytes() = %v, want %v", got, want)
	}
}

func TestTTLIsEnforcedAtReadAndLazilyDeleted(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	db, storage := testDB(t, func(cfg *config) error {
		cfg.now = func() time.Time { return now }
		return nil
	})
	ctx := context.Background()
	if err := db.Put(ctx, "session", "alive", TTL(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.Get(ctx, "session", &value); err != nil || value != "alive" {
		t.Fatalf("before expiry: value=%q err=%v", value, err)
	}
	now = now.Add(time.Minute)
	if err := db.Get(ctx, "session", &value); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after expiry: got %v, want ErrNotFound", err)
	}
	_, found, err := storage.Get(ctx, valueKey("session"))
	if err != nil || found {
		t.Fatalf("expired record was not lazily deleted: found=%v err=%v", found, err)
	}
}

func TestHashSetAndListCollections(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	if err := db.HSet(ctx, "profile:101", "name", "Ada"); err != nil {
		t.Fatal(err)
	}
	if err := db.HSet(ctx, "profile:101", "score", 42); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := db.HGetAll(ctx, "profile:101", &fields); err != nil {
		t.Fatal(err)
	}
	if fields["name"] != "Ada" || fields["score"] != float64(42) {
		t.Fatalf("unexpected hash: %#v", fields)
	}
	added, err := db.SAdd(ctx, "roles", "admin", "author", "admin")
	if err != nil || added != 2 {
		t.Fatalf("SAdd() = %d, %v", added, err)
	}
	members, err := db.SMembers(ctx, "roles")
	if err != nil || !reflectStrings(members, []string{"admin", "author"}) {
		t.Fatalf("SMembers() = %#v, %v", members, err)
	}
	if _, err := db.RPush(ctx, "jobs", "one", "two", "three"); err != nil {
		t.Fatal(err)
	}
	var jobs []string
	if err := db.LRange(ctx, "jobs", 1, -1, &jobs); err != nil {
		t.Fatal(err)
	}
	if !reflectStrings(jobs, []string{"two", "three"}) {
		t.Fatalf("LRange() = %#v", jobs)
	}
}

func reflectStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestSharingRoundTripAndAuthentication(t *testing.T) {
	owner, _ := testDB(t, WithSharing(SharingOptions{
		ListenAddress: "127.0.0.1:0",
		BearerToken:   "secret",
	}))
	remote, err := OpenRemote(owner.SharingAddress(), RemoteOptions{BearerToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	ctx := context.Background()
	if err := remote.Put(ctx, "shared", map[string]int{"answer": 42}); err != nil {
		t.Fatal(err)
	}
	var value map[string]int
	if err := owner.Get(ctx, "shared", &value); err != nil {
		t.Fatal(err)
	}
	if value["answer"] != 42 {
		t.Fatalf("unexpected remote value: %#v", value)
	}
	if length, err := remote.RPush(ctx, "remote-jobs", "one", "two"); err != nil || length != 2 {
		t.Fatalf("remote RPush() = %d, %v", length, err)
	}
	var jobs []string
	if err := owner.LRange(ctx, "remote-jobs", 0, -1, &jobs); err != nil || !reflectStrings(jobs, []string{"one", "two"}) {
		t.Fatalf("owner LRange() = %#v, %v", jobs, err)
	}
	unauthorized, err := OpenRemote(owner.SharingAddress(), RemoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorized.Close()
	if err := unauthorized.Get(ctx, "shared", &value); err == nil {
		t.Fatal("unauthenticated read unexpectedly succeeded")
	}
}

func TestPublicJSONHTTPAPI(t *testing.T) {
	owner, _ := testDB(t, WithSharing(SharingOptions{ListenAddress: "127.0.0.1:0"}))
	baseURL := owner.SharingAddress()
	key := base64.RawURLEncoding.EncodeToString([]byte("http-key"))
	putRequest, err := http.NewRequest(http.MethodPut, baseURL+"/v1/entries/"+key+"?ttl_seconds=60", bytes.NewBufferString(`{"answer":42}`))
	if err != nil {
		t.Fatal(err)
	}
	putRequest.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(putRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %s", response.Status)
	}
	response, err = http.Get(baseURL + "/v1/entries/" + key)
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
	discovery, err := http.Get(baseURL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.Body.Close()
	if discovery.StatusCode != http.StatusOK {
		t.Fatalf("discovery status = %s", discovery.Status)
	}
	var metadata struct {
		Backend Backend `json:"backend"`
	}
	if err := json.NewDecoder(discovery.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Backend != BackendRocksDB {
		t.Fatalf("discovery backend = %q, want %q", metadata.Backend, BackendRocksDB)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	db, storage := testDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if !storage.closed {
		t.Fatal("storage was not closed")
	}
	if err := db.Put(context.Background(), "key", "value"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Put after Close = %v, want ErrClosed", err)
	}
}
