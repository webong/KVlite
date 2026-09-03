package kvliteredis

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/webong/kvlite"
)

const (
	redisTypeNone   = "none"
	redisTypeString = "string"
	redisTypeHash   = "hash"
	redisTypeSet    = "set"
	redisTypeList   = "list"
)

const bytesCodec = "bytes"

var errRedisWrongType = errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")

// database adapts KVLite's stable protocol-store contract to Redis command
// semantics. Keeping it private makes the only public extension entry point
// Serve and avoids exposing RESP implementation details to embedded users.
type database struct {
	store  kvlite.ProtocolStore
	engine kvlite.TransportStore
	cfg    redisConfig
}

type redisConfig struct {
	now func() time.Time
}

type redisRecord struct {
	expiresAt int64
	codec     string
	payload   []byte
}

func newDatabase(store kvlite.ProtocolStore) *database {
	return &database{
		store:  store,
		engine: store,
		cfg:    redisConfig{now: store.Now},
	}
}

func (db *database) putPayload(ctx context.Context, key []byte, codec string, payload []byte, ttl time.Duration) error {
	var expiresAt int64
	if ttl > 0 {
		expiresAt = db.cfg.now().Add(ttl).UnixNano()
	}
	encoded, err := db.store.EncodeRecord(codec, payload, expiresAt)
	if err != nil {
		return err
	}
	return db.engine.Put(ctx, key, encoded)
}

func (db *database) decodeRecord(data []byte) (redisRecord, error) {
	value, err := db.store.DecodeRecord(data)
	if err != nil {
		return redisRecord{}, err
	}
	return redisRecord{expiresAt: value.ExpiresAt, codec: value.Codec, payload: value.Payload}, nil
}

func (db *database) encodeRecord(codec string, payload []byte, expiresAt int64) ([]byte, error) {
	return db.store.EncodeRecord(codec, payload, expiresAt)
}

func (db *database) redisKeyExpired(ctx context.Context, key string) (bool, error) {
	expiresAt, found, err := db.redisMetadataExpiry(ctx, key)
	if err != nil || !found {
		return false, err
	}
	if db.cfg.now().UnixNano() < expiresAt {
		return false, nil
	}
	_, err = db.redisDeleteRaw(ctx, key)
	return true, err
}

func (db *database) redisMetadataExpiry(ctx context.Context, key string) (int64, bool, error) {
	data, found, err := db.engine.Get(ctx, db.store.CollectionTTLKey(key))
	if err != nil || !found {
		return 0, found, err
	}
	if len(data) != 8 {
		return 0, false, fmt.Errorf("kvlite: invalid Redis expiry metadata")
	}
	return int64(binary.BigEndian.Uint64(data)), true, nil
}

func encodeRedisExpiry(expiresAt int64) []byte {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, uint64(expiresAt))
	return data
}

func (db *database) redisType(ctx context.Context, key string) (string, error) {
	expired, err := db.redisKeyExpired(ctx, key)
	if err != nil {
		return redisTypeNone, err
	}
	if expired {
		return redisTypeNone, nil
	}

	if data, found, err := db.engine.Get(ctx, db.store.ValueKey(key)); err != nil {
		return redisTypeNone, err
	} else if found {
		value, err := db.decodeRecord(data)
		if err != nil {
			return redisTypeNone, err
		}
		if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
			_ = db.engine.Delete(ctx, db.store.ValueKey(key))
		} else {
			return redisTypeString, nil
		}
	}

	hashPrefix := db.store.HashPrefix(key)
	var hashKeys [][]byte
	hashFound := false
	if err := db.engine.ScanPrefix(ctx, hashPrefix, func(storageKey, data []byte) error {
		value, err := db.decodeRecord(data)
		if err != nil {
			return err
		}
		if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
			hashKeys = append(hashKeys, append([]byte(nil), storageKey...))
			return nil
		}
		hashFound = true
		return nil
	}); err != nil {
		return redisTypeNone, err
	}
	for _, storageKey := range hashKeys {
		if err := db.engine.Delete(ctx, storageKey); err != nil {
			return redisTypeNone, err
		}
	}
	if hashFound {
		return redisTypeHash, nil
	}
	if len(hashKeys) > 0 {
		// A hash whose last field expired no longer exists. Remove collection
		// metadata as well so a later key creation starts without a stale TTL.
		if _, err := db.redisDeleteRaw(ctx, key); err != nil {
			return redisTypeNone, err
		}
	}

	setFound := false
	if err := db.engine.ScanPrefix(ctx, db.store.SetPrefix(key), func(_, _ []byte) error {
		setFound = true
		return nil
	}); err != nil {
		return redisTypeNone, err
	}
	if setFound {
		return redisTypeSet, nil
	}

	if data, found, err := db.engine.Get(ctx, db.store.ListKey(key)); err != nil {
		return redisTypeNone, err
	} else if found {
		items, err := db.store.DecodeList(data)
		if err != nil {
			return redisTypeNone, err
		}
		if len(items) > 0 {
			return redisTypeList, nil
		}
		_, _ = db.redisDeleteRaw(ctx, key)
	}
	return redisTypeNone, nil
}

func (db *database) redisDeleteRaw(ctx context.Context, key string) (bool, error) {
	return db.store.DeleteLogicalKey(ctx, key)
}

func (db *database) redisString(ctx context.Context, key string) ([]byte, bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if typ == redisTypeNone {
		return nil, false, nil
	}
	if typ != redisTypeString {
		return nil, false, fmt.Errorf("%w: %s", errRedisWrongType, typ)
	}
	data, found, err := db.engine.Get(ctx, db.store.ValueKey(key))
	if err != nil || !found {
		return nil, false, err
	}
	value, err := db.decodeRecord(data)
	if err != nil {
		return nil, false, err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		_, _ = db.redisDeleteRaw(ctx, key)
		return nil, false, nil
	}
	return append([]byte(nil), value.payload...), true, nil
}

func (db *database) redisWriteString(ctx context.Context, key string, value []byte, expiresAt int64) error {
	if expiresAt <= 0 {
		if err := db.putPayload(ctx, db.store.ValueKey(key), bytesCodec, value, 0); err != nil {
			return err
		}
		// Strings keep expiry in their envelope; collection metadata must not
		// linger when a key changes type or is recreated.
		return db.engine.Delete(ctx, db.store.CollectionTTLKey(key))
	}
	duration := time.Unix(0, expiresAt).Sub(db.cfg.now())
	if duration <= 0 {
		_, err := db.redisDeleteRaw(ctx, key)
		return err
	}
	return db.putPayload(ctx, db.store.ValueKey(key), bytesCodec, value, duration)
}

func (db *database) redisExpiry(ctx context.Context, key string) (int64, bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil || typ == redisTypeNone {
		return 0, false, err
	}
	if typ == redisTypeString {
		data, found, err := db.engine.Get(ctx, db.store.ValueKey(key))
		if err != nil || !found {
			return 0, false, err
		}
		value, err := db.decodeRecord(data)
		if err != nil {
			return 0, false, err
		}
		if value.expiresAt > 0 {
			return value.expiresAt, true, nil
		}
		return 0, false, nil
	}
	return db.redisMetadataExpiry(ctx, key)
}

func (db *database) redisSetExpiry(ctx context.Context, key string, expiresAt int64) (bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil || typ == redisTypeNone {
		return false, err
	}
	if expiresAt <= db.cfg.now().UnixNano() {
		_, err := db.redisDeleteRaw(ctx, key)
		return err == nil, err
	}
	if typ == redisTypeString {
		data, found, err := db.engine.Get(ctx, db.store.ValueKey(key))
		if err != nil || !found {
			return false, err
		}
		value, err := db.decodeRecord(data)
		if err != nil {
			return false, err
		}
		if err := db.redisWriteStringWithCodec(ctx, key, value.payload, value.codec, expiresAt); err != nil {
			return false, err
		}
		_ = db.engine.Delete(ctx, db.store.CollectionTTLKey(key))
		return true, nil
	}
	if err := db.engine.Put(ctx, db.store.CollectionTTLKey(key), encodeRedisExpiry(expiresAt)); err != nil {
		return false, err
	}
	return true, nil
}

func (db *database) redisWriteStringWithCodec(ctx context.Context, key string, payload []byte, codec string, expiresAt int64) error {
	if expiresAt <= 0 {
		if err := db.putPayload(ctx, db.store.ValueKey(key), codec, payload, 0); err != nil {
			return err
		}
		return db.engine.Delete(ctx, db.store.CollectionTTLKey(key))
	}
	duration := time.Unix(0, expiresAt).Sub(db.cfg.now())
	if duration <= 0 {
		_, err := db.redisDeleteRaw(ctx, key)
		return err
	}
	return db.putPayload(ctx, db.store.ValueKey(key), codec, payload, duration)
}

func (db *database) redisPersist(ctx context.Context, key string) (bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil || typ == redisTypeNone {
		return false, err
	}
	_, found, err := db.redisExpiry(ctx, key)
	if err != nil || !found {
		return false, err
	}
	if typ == redisTypeString {
		data, exists, err := db.engine.Get(ctx, db.store.ValueKey(key))
		if err != nil || !exists {
			return false, err
		}
		value, err := db.decodeRecord(data)
		if err != nil {
			return false, err
		}
		if err := db.putPayload(ctx, db.store.ValueKey(key), value.codec, value.payload, 0); err != nil {
			return false, err
		}
	} else if err := db.engine.Delete(ctx, db.store.CollectionTTLKey(key)); err != nil {
		return false, err
	}
	return true, nil
}

func (db *database) redisHashField(ctx context.Context, key, field string) ([]byte, bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if typ == redisTypeNone {
		return nil, false, nil
	}
	if typ != redisTypeHash {
		return nil, false, fmt.Errorf("%w: %s", errRedisWrongType, typ)
	}
	storageKey := db.store.HashKey(key, field)
	data, found, err := db.engine.Get(ctx, storageKey)
	if err != nil || !found {
		return nil, false, err
	}
	value, err := db.decodeRecord(data)
	if err != nil {
		return nil, false, err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		_ = db.engine.Delete(ctx, storageKey)
		return nil, false, nil
	}
	return append([]byte(nil), value.payload...), true, nil
}

func (db *database) redisList(ctx context.Context, key string) ([][]byte, bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if typ == redisTypeNone {
		return nil, false, nil
	}
	if typ != redisTypeList {
		return nil, false, fmt.Errorf("%w: %s", errRedisWrongType, typ)
	}
	data, found, err := db.engine.Get(ctx, db.store.ListKey(key))
	if err != nil || !found {
		return nil, false, err
	}
	items, err := db.store.DecodeList(data)
	if err != nil {
		return nil, false, err
	}
	return items, true, nil
}

func (db *database) redisItemPayload(data []byte, now time.Time) ([]byte, bool, error) {
	value, err := db.decodeRecord(data)
	if err != nil {
		return nil, false, err
	}
	if value.expiresAt > 0 && now.UnixNano() >= value.expiresAt {
		return nil, false, nil
	}
	return append([]byte(nil), value.payload...), true, nil
}

func (db *database) redisWriteList(ctx context.Context, key string, items [][]byte) error {
	if len(items) == 0 {
		_, err := db.redisDeleteRaw(ctx, key)
		return err
	}
	return db.engine.Put(ctx, db.store.ListKey(key), db.store.EncodeList(items))
}
