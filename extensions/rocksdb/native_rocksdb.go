//go:build rocksdb

package rocksdb

import (
	"bytes"
	"context"
	"fmt"

	"github.com/linxGnu/grocksdb"
	"github.com/webong/kvlite"
)

func nativeAvailable() error { return nil }

type engine struct {
	db      *grocksdb.DB
	options *grocksdb.Options
	read    *grocksdb.ReadOptions
	write   *grocksdb.WriteOptions
	blocks  *grocksdb.BlockBasedTableOptions
	cache   *grocksdb.Cache
}

type expiryCompactionFilter struct {
	recordExpired func([]byte) bool
}

func (filter *expiryCompactionFilter) Filter(_ int, _ []byte, value []byte) (bool, []byte) {
	return filter.recordExpired(value), nil
}

func (*expiryCompactionFilter) Name() string            { return "kvlite.expiry.v1" }
func (*expiryCompactionFilter) SetIgnoreSnapshots(bool) {}
func (*expiryCompactionFilter) Destroy()                {}

func openNative(path string, config kvlite.DriverOptions) (kvlite.Engine, error) {
	options := grocksdb.NewDefaultOptions()
	options.SetCreateIfMissing(true)
	options.SetWriteBufferSize(uint64(config.WriteBufferSize))
	options.SetMaxWriteBufferNumber(config.MaxWriteBuffers)
	options.SetMinWriteBufferNumberToMerge(1)
	options.SetMaxBackgroundJobs(config.MaxBackgroundJobs)
	options.SetBytesPerSync(1 << 20)
	options.SetLevelCompactionDynamicLevelBytes(true)
	options.SetPeriodicCompactionSeconds(3600)
	options.SetCompression(grocksdb.LZ4Compression)
	if config.RecordExpired != nil {
		options.SetCompactionFilter(&expiryCompactionFilter{recordExpired: config.RecordExpired})
	}

	cache := grocksdb.NewLRUCache(uint64(config.BlockCacheSize))
	blocks := grocksdb.NewDefaultBlockBasedTableOptions()
	blocks.SetBlockCache(cache)
	blocks.SetBlockSize(16 << 10)
	blocks.SetCacheIndexAndFilterBlocks(true)
	blocks.SetPinL0FilterAndIndexBlocksInCache(true)
	blocks.SetFilterPolicy(grocksdb.NewBloomFilterFull(10))
	options.SetBlockBasedTableFactory(blocks)

	database, err := grocksdb.OpenDb(options, path)
	if err != nil {
		options.Destroy()
		blocks.Destroy()
		cache.Destroy()
		return nil, fmt.Errorf("kvlite: open RocksDB: %w", err)
	}
	read := grocksdb.NewDefaultReadOptions()
	write := grocksdb.NewDefaultWriteOptions()
	return &engine{
		db: database, options: options, read: read, write: write,
		blocks: blocks, cache: cache,
	}, nil
}

func (engine *engine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	value, err := engine.db.Get(engine.read, key)
	if err != nil {
		return nil, false, err
	}
	defer value.Free()
	if !value.Exists() {
		return nil, false, nil
	}
	return append([]byte(nil), value.Data()...), true, nil
}

func (engine *engine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return engine.db.Put(engine.write, key, value)
}

func (engine *engine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return engine.db.Delete(engine.write, key)
}

func (engine *engine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	iterator := engine.db.NewIterator(engine.read)
	defer iterator.Close()
	for iterator.Seek(prefix); iterator.Valid(); iterator.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		keySlice := iterator.Key()
		valueSlice := iterator.Value()
		key := append([]byte(nil), keySlice.Data()...)
		value := append([]byte(nil), valueSlice.Data()...)
		keySlice.Free()
		valueSlice.Free()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		if err := callback(key, value); err != nil {
			return err
		}
	}
	return iterator.Err()
}

func (engine *engine) Close() error {
	engine.db.Close()
	engine.read.Destroy()
	engine.write.Destroy()
	engine.options.Destroy()
	engine.blocks.Destroy()
	engine.cache.Destroy()
	return nil
}
