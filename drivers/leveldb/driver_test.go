package leveldb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/kvlite"
	_ "github.com/webong/kvlite/drivers/leveldb"
)

func TestLevelDBDriverOpensAndPersistsKVLiteRecords(t *testing.T) {
	if got := kvlite.DefaultDriver(); got != kvlite.DriverLevelDB {
		t.Fatalf("DefaultDriver() = %q, want %q", got, kvlite.DriverLevelDB)
	}
	path := t.TempDir()
	database, err := kvlite.Open(path, kvlite.WithDriver("leveldb"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put(context.Background(), "user:101", map[string]string{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	manifestData, err := os.ReadFile(filepath.Join(path, "KVLITE-MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Backend string `json:"backend"`
		Driver  string `json:"driver"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Backend != "leveldb" || manifest.Driver != "goleveldb" {
		t.Fatalf("unexpected driver manifest: %#v", manifest)
	}

	reopened, err := kvlite.Open(path, kvlite.WithDriver("leveldb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var user map[string]string
	if err := reopened.Get(context.Background(), "user:101", &user); err != nil {
		t.Fatal(err)
	}
	if user["name"] != "Ada" {
		t.Fatalf("reopened value = %#v", user)
	}
}
