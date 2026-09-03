//go:build berkeleydb && cgo

package berkeleydb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/webong/kvlite"
	"github.com/webong/kvlite/extensions/berkeleydb"
)

func TestBerkeleyDBDriverOpensPersistsAndScansKVLiteRecords(t *testing.T) {
	if got := kvlite.DefaultDriver(); got != kvlite.DriverBerkeleyDB {
		t.Fatalf("DefaultDriver() = %q, want %q", got, kvlite.DriverBerkeleyDB)
	}
	path := t.TempDir()
	database, err := kvlite.Open(path, kvlite.WithDriver("berkeleydb"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Put(context.Background(), "user:101", map[string]string{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SAdd(context.Background(), "user:101:roles", "admin", "author"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(path, berkeleydb.DatabaseFilename)); err != nil {
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
	if manifest.Backend != "berkeleydb" || manifest.Driver != "berkeleydb-c" {
		t.Fatalf("unexpected driver manifest: %#v", manifest)
	}

	reopened, err := kvlite.Open(path, kvlite.WithDriver("berkeleydb"))
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
	roles, err := reopened.SMembers(context.Background(), "user:101:roles")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roles, []string{"admin", "author"}) {
		t.Fatalf("SMembers() = %#v", roles)
	}
}
