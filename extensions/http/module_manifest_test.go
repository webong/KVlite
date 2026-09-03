package kvlitehttp

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/webong/kvlite"
)

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
	if !reflect.DeepEqual(manifest, Manifest()) {
		t.Fatalf("source manifest = %#v, linked manifest = %#v", manifest, Manifest())
	}
	linked, err := kvlite.LinkedModule("http")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(linked.Manifest, manifest) {
		t.Fatalf("linked module = %#v, manifest = %#v", linked.Manifest, manifest)
	}
}
