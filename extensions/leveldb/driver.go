// Package leveldb registers KVLite's optional pure-Go LevelDB driver extension.
//
// Import it for its side effect before opening a LevelDB-backed database:
//
//	import _ "github.com/webong/kvlite/extensions/leveldb"
package leveldb

import (
	"context"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"
	leveldbErrors "github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/filter"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/webong/kvlite"
)

// Name is the stable KVLite driver selection name.
const Name kvlite.DriverName = kvlite.DriverLevelDB

type driver struct{}

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
	kvlite.MustRegisterDriver(driver{})
}

// Manifest describes this linked Go driver using the same metadata shape as a
// standalone KVLite module bundle. Release artifacts supply checksummed native
// entries through kvlite-module.json; this source module remains the explicit
// Go-import development path.
func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          string(Name),
		Kind:          kvlite.ModuleKindDriver,
		Version:       "v0.1.0",
		ModuleABI:     kvlite.ModuleABIVersion,
		Driver:        Name,
		Capabilities:  []string{"embedded-storage"},
		License:       "Apache-2.0",
	}
}

func (driver) Info() kvlite.DriverInfo {
	return kvlite.DriverInfo{
		Driver:         Name,
		Implementation: "goleveldb",
		Format:         "v1",
		Version:        "v1.0.0",
	}
}

func (driver) Available() error { return nil }

func (driver) Open(path string, options kvlite.DriverOptions) (kvlite.Engine, error) {
	database, err := leveldb.OpenFile(path, &opt.Options{
		BlockCacheCapacity: options.BlockCacheSize,
		WriteBuffer:        options.WriteBufferSize,
		Filter:             filter.NewBloomFilter(10),
	})
	if err != nil {
		return nil, fmt.Errorf("kvlite: open LevelDB: %w", err)
	}
	return &engine{db: database}, nil
}

type engine struct {
	db *leveldb.DB
}

func (engine *engine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	value, err := engine.db.Get(key, nil)
	if err == leveldbErrors.ErrNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]byte(nil), value...), true, nil
}

func (engine *engine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return engine.db.Put(key, value, nil)
}

func (engine *engine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return engine.db.Delete(key, nil)
}

func (engine *engine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	iterator := engine.db.NewIterator(util.BytesPrefix(prefix), nil)
	defer iterator.Release()
	for iterator.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := append([]byte(nil), iterator.Key()...)
		value := append([]byte(nil), iterator.Value()...)
		if err := callback(key, value); err != nil {
			return err
		}
	}
	return iterator.Error()
}

func (engine *engine) Close() error {
	return engine.db.Close()
}
