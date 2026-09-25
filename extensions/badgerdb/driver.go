// Package badgerdb registers KVLite's optional BadgerDB storage driver.
// Import it for its side effect before opening with WithDriver("badgerdb").
package badgerdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v4"
	"github.com/webong/kvlite"
)

const Name kvlite.DriverName = kvlite.DriverBadgerDB

type driver struct{}

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
	kvlite.MustRegisterDriver(driver{})
}

func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          string(Name), Kind: kvlite.ModuleKindDriver,
		Version: "v0.1.0", ModuleABI: kvlite.ModuleABIVersion,
		Driver: Name, Capabilities: []string{"embedded-storage"}, License: "Apache-2.0",
	}
}

func (driver) Info() kvlite.DriverInfo {
	return kvlite.DriverInfo{Driver: Name, Implementation: "badger-go", Format: "badger-v4", Version: "v4.8.0"}
}

func (driver) Available() error { return nil }

func (driver) Open(path string, options kvlite.DriverOptions) (kvlite.Engine, error) {
	settings := badger.DefaultOptions(path).
		WithLogger(nil).
		WithMetricsEnabled(false).
		WithSyncWrites(true).
		WithNumMemtables(3).
		WithNumCompactors(2).
		WithNumGoroutines(2)
	if options.WriteBufferSize > 0 {
		settings = settings.WithMemTableSize(int64(options.WriteBufferSize))
	}
	if options.BlockCacheSize > 0 {
		settings = settings.WithBlockCacheSize(int64(options.BlockCacheSize))
	}
	db, err := badger.Open(settings)
	if err != nil {
		return nil, fmt.Errorf("kvlite: open BadgerDB: %w", err)
	}
	return &engine{db: db}, nil
}

type engine struct{ db *badger.DB }

func (e *engine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	var value []byte
	err := e.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		value, err = item.ValueCopy(nil)
		return err
	})
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (e *engine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.db.Update(func(txn *badger.Txn) error { return txn.Set(key, value) })
}

func (e *engine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.db.Update(func(txn *badger.Txn) error { return txn.Delete(key) })
}

func (e *engine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.db.View(func(txn *badger.Txn) error {
		iterator := txn.NewIterator(badger.DefaultIteratorOptions)
		defer iterator.Close()
		for iterator.Seek(prefix); iterator.Valid(); iterator.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			item := iterator.Item()
			key := item.KeyCopy(nil)
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			value, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			if err := callback(key, value); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *engine) Close() error { return e.db.Close() }
