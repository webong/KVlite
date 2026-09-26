// Package boltdb registers KVLite's optional BoltDB-compatible driver,
// implemented with the maintained go.etcd.io/bbolt fork.
package boltdb

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/webong/kvlite"
	bolt "go.etcd.io/bbolt"
)

const Name kvlite.DriverName = kvlite.DriverBoltDB
const DatabaseFilename = "KVLITE-BOLT.db"

var recordsBucket = []byte("kvlite-records")

type driver struct{}

func init() {
	kvlite.MustRegisterLinkedModule(Manifest())
	kvlite.MustRegisterDriver(driver{})
}

func Manifest() kvlite.ModuleManifest {
	return kvlite.ModuleManifest{
		SchemaVersion: kvlite.ModuleManifestVersion,
		Name:          string(Name), Kind: kvlite.ModuleKindEngine,
		Version: "v0.1.0", ModuleABI: kvlite.ModuleABIVersion,
		Driver: Name, Capabilities: []string{"embedded-storage"}, License: "MIT",
	}
}

func (driver) Info() kvlite.DriverInfo {
	return kvlite.DriverInfo{Driver: Name, Implementation: "bbolt", Format: "bbolt-v1", Version: "v1.4.3"}
}

func (driver) Available() error { return nil }

func (driver) Open(path string, _ kvlite.DriverOptions) (kvlite.Engine, error) {
	db, err := bolt.Open(filepath.Join(path, DatabaseFilename), 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("kvlite: open BoltDB: %w", err)
	}
	err = db.Update(func(txn *bolt.Tx) error { _, err := txn.CreateBucketIfNotExists(recordsBucket); return err })
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("kvlite: initialize BoltDB: %w", err)
	}
	return &engine{db: db}, nil
}

type engine struct{ db *bolt.DB }

func (e *engine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	var value []byte
	var found bool
	err := e.db.View(func(txn *bolt.Tx) error {
		stored := txn.Bucket(recordsBucket).Get(key)
		if stored != nil {
			value = append([]byte(nil), stored...)
			found = true
		}
		return nil
	})
	return value, found, err
}

func (e *engine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.db.Update(func(txn *bolt.Tx) error { return txn.Bucket(recordsBucket).Put(key, value) })
}

func (e *engine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.db.Update(func(txn *bolt.Tx) error { return txn.Bucket(recordsBucket).Delete(key) })
}

func (e *engine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.db.View(func(txn *bolt.Tx) error {
		cursor := txn.Bucket(recordsBucket).Cursor()
		for key, value := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := callback(append([]byte(nil), key...), append([]byte(nil), value...)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (e *engine) Close() error { return e.db.Close() }
