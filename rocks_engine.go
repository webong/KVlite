//go:build rocksdb

package kvlite

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/linxGnu/grocksdb"
)

type rocksEngine struct {
	db      *grocksdb.DB
	options *grocksdb.Options
	read    *grocksdb.ReadOptions
	write   *grocksdb.WriteOptions
	blocks  *grocksdb.BlockBasedTableOptions
	cache   *grocksdb.Cache
}

type expiryCompactionFilter struct {
	now func() time.Time
}

func (filter *expiryCompactionFilter) Filter(_ int, _ []byte, value []byte) (bool, []byte) {
	return envelopeExpired(value, filter.now()), nil
}

func (*expiryCompactionFilter) Name() string            { return "kvlite.expiry.v1" }
func (*expiryCompactionFilter) SetIgnoreSnapshots(bool) {}
func (*expiryCompactionFilter) Destroy()                {}

func openRocksEngine(path string, cfg config) (engine, error) {
	options := grocksdb.NewDefaultOptions()
	options.SetCreateIfMissing(true)
	options.SetWriteBufferSize(uint64(cfg.writeBufferSize))
	options.SetMaxWriteBufferNumber(cfg.maxWriteBuffers)
	options.SetMinWriteBufferNumberToMerge(1)
	options.SetMaxBackgroundJobs(cfg.maxBackgroundJobs)
	options.SetBytesPerSync(1 << 20)
	options.SetLevelCompactionDynamicLevelBytes(true)
	options.SetPeriodicCompactionSeconds(3600)
	options.SetCompression(grocksdb.LZ4Compression)
	options.SetCompactionFilter(&expiryCompactionFilter{now: cfg.now})

	cache := grocksdb.NewLRUCache(uint64(cfg.blockCacheSize))
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
	return &rocksEngine{
		db: database, options: options, read: read, write: write,
		blocks: blocks, cache: cache,
	}, nil
}

func (engine *rocksEngine) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
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

func (engine *rocksEngine) Put(ctx context.Context, key, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return engine.db.Put(engine.write, key, value)
}

func (engine *rocksEngine) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return engine.db.Delete(engine.write, key)
}

func (engine *rocksEngine) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
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

func (engine *rocksEngine) Close() error {
	engine.db.Close()
	engine.read.Destroy()
	engine.write.Destroy()
	engine.options.Destroy()
	engine.blocks.Destroy()
	engine.cache.Destroy()
	return nil
}
