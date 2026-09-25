package badgerdb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/webong/kvlite"
	"github.com/webong/kvlite/extensions/badgerdb"
)

func TestDriverPersistsCollectionsAndRecords(t *testing.T) {
	path := t.TempDir()
	ctx := context.Background()
	db, err := kvlite.Open(path, kvlite.WithDriver("badgerdb"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "user:101", map[string]string{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SAdd(ctx, "roles", "admin", "author"); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
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
	if manifest.Backend != "badgerdb" || manifest.Driver != "badger-go" {
		t.Fatalf("manifest: %#v", manifest)
	}

	db, err = kvlite.Open(path, kvlite.WithDriver("badgerdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var user map[string]string
	if err := db.Get(ctx, "user:101", &user); err != nil {
		t.Fatal(err)
	}
	if user["name"] != "Ada" {
		t.Fatalf("user: %#v", user)
	}
	roles, err := db.SMembers(ctx, "roles")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(roles, []string{"admin", "author"}) {
		t.Fatalf("roles: %v", roles)
	}
}

func TestModuleManifestMatchesLinkedMetadata(t *testing.T) {
	data, err := os.ReadFile("kvlite-module.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest kvlite.ModuleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest, badgerdb.Manifest()) {
		t.Fatalf("module mismatch: %#v", manifest)
	}
}
