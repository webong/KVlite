//go:build cgo

package lmdb

import (
	"bytes"
	"context"
	"fmt"

	native "github.com/PowerDNS/lmdb-go/lmdb"
	"github.com/webong/kvlite"
)

// LMDB's map size is a virtual address-space reservation, not an immediate
// disk allocation on the supported 64-bit platforms. The fixed map avoids
// unsafe automatic resize while other processes may hold reader mappings.
const mapSize = int64(16 << 30)

func nativeAvailable() error { return nil }

func openNative(path string) (kvlite.Engine, error) {
	env, err := native.NewEnv()
	if err != nil {
		return nil, fmt.Errorf("kvlite: create LMDB environment: %w", err)
	}
	if err := env.SetMapSize(mapSize); err != nil {
		_ = env.Close()
		return nil, fmt.Errorf("kvlite: configure LMDB map: %w", err)
	}
	if err := env.Open(path, 0, 0o600); err != nil {
		_ = env.Close()
		return nil, fmt.Errorf("kvlite: open LMDB: %w", err)
	}
	// Use the unnamed/root DBI, which needs no write transaction to initialize.
	var dbi native.DBI
	err = env.View(func(txn *native.Txn) error {
		var openErr error
		dbi, openErr = txn.OpenRoot(0)
		return openErr
	})
	if err != nil {
		_ = env.Close()
		return nil, fmt.Errorf("kvlite: open LMDB root database: %w", err)
	}
	return &engine{env: env, dbi: dbi, maxKeySize: env.MaxKeySize()}, nil
}

type engine struct {
	env        *native.Env
	dbi        native.DBI
	maxKeySize int
}

func (e *engine) validateKey(key []byte) error {
	if len(key) > e.maxKeySize {
		return fmt.Errorf("kvlite: LMDB storage key is %d bytes; maximum is %d", len(key), e.maxKeySize)
	}
	return nil
}

func (e *engine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := e.validateKey(key); err != nil {
		return nil, false, err
	}
	var value []byte
	err := e.env.View(func(txn *native.Txn) error {
		stored, err := txn.Get(e.dbi, key)
		if err != nil {
			return err
		}
		value = append([]byte(nil), stored...)
		return nil
	})
	if native.IsNotFound(err) {
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
	if err := e.validateKey(key); err != nil {
		return err
	}
	err := e.env.Update(func(txn *native.Txn) error { return txn.Put(e.dbi, key, value, 0) })
	if native.IsMapFull(err) {
		return fmt.Errorf("kvlite: LMDB map is full (16 GiB virtual limit): %w", err)
	}
	return err
}

func (e *engine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.validateKey(key); err != nil {
		return err
	}
	err := e.env.Update(func(txn *native.Txn) error { return txn.Del(e.dbi, key, nil) })
	if native.IsNotFound(err) {
		return nil
	}
	return err
}

func (e *engine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(prefix) > e.maxKeySize {
		return nil
	}
	return e.env.View(func(txn *native.Txn) error {
		cursor, err := txn.OpenCursor(e.dbi)
		if err != nil {
			return err
		}
		defer cursor.Close()
		var key, value []byte
		if len(prefix) == 0 {
			key, value, err = cursor.Get(nil, nil, native.First)
		} else {
			key, value, err = cursor.Get(prefix, nil, native.SetRange)
		}
		for err == nil && bytes.HasPrefix(key, prefix) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := callback(append([]byte(nil), key...), append([]byte(nil), value...)); err != nil {
				return err
			}
			key, value, err = cursor.Get(nil, nil, native.Next)
		}
		if native.IsNotFound(err) {
			return nil
		}
		return err
	})
}

func (e *engine) Close() error { return e.env.Close() }
