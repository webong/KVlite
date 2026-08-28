package kvlite

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"
)

const (
	redisTypeNone   = "none"
	redisTypeString = "string"
	redisTypeHash   = "hash"
	redisTypeSet    = "set"
	redisTypeList   = "list"
)

func (db *DB) redisKeyExpired(ctx context.Context, key string) (bool, error) {
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

func (db *DB) redisMetadataExpiry(ctx context.Context, key string) (int64, bool, error) {
	data, found, err := db.engine.Get(ctx, redisTTLKey(key))
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

func (db *DB) redisType(ctx context.Context, key string) (string, error) {
	expired, err := db.redisKeyExpired(ctx, key)
	if err != nil {
		return redisTypeNone, err
	}
	if expired {
		return redisTypeNone, nil
	}

	if data, found, err := db.engine.Get(ctx, valueKey(key)); err != nil {
		return redisTypeNone, err
	} else if found {
		value, err := unmarshalEnvelope(data)
		if err != nil {
			return redisTypeNone, err
		}
		if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
			_ = db.engine.Delete(ctx, valueKey(key))
		} else {
			return redisTypeString, nil
		}
	}

	hashPrefix := namespacePrefix(kindHash, key)
	var hashKeys [][]byte
	hashFound := false
	if err := db.engine.ScanPrefix(ctx, hashPrefix, func(storageKey, data []byte) error {
		value, err := unmarshalEnvelope(data)
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
	if err := db.engine.ScanPrefix(ctx, namespacePrefix(kindSet, key), func(_, _ []byte) error {
		setFound = true
		return nil
	}); err != nil {
		return redisTypeNone, err
	}
	if setFound {
		return redisTypeSet, nil
	}

	if data, found, err := db.engine.Get(ctx, listKey(key)); err != nil {
		return redisTypeNone, err
	} else if found {
		items, err := decodeList(data)
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

func (db *DB) redisDeleteRaw(ctx context.Context, key string) (bool, error) {
	// Value, list, and expiry records are exact keys. Scanning their prefixes
	// would also remove a sibling such as "user:10" when deleting "user:1".
	keys := make([][]byte, 0, 4)
	for _, storageKey := range [][]byte{valueKey(key), listKey(key), redisTTLKey(key)} {
		_, found, err := db.engine.Get(ctx, storageKey)
		if err != nil {
			return false, err
		}
		if found {
			keys = append(keys, storageKey)
		}
	}
	for _, prefix := range [][]byte{namespacePrefix(kindHash, key), namespacePrefix(kindSet, key)} {
		if err := db.engine.ScanPrefix(ctx, prefix, func(storageKey, _ []byte) error {
			keys = append(keys, append([]byte(nil), storageKey...))
			return nil
		}); err != nil {
			return false, err
		}
	}
	for _, storageKey := range keys {
		if err := db.engine.Delete(ctx, storageKey); err != nil {
			return false, err
		}
	}
	return len(keys) > 0, nil
}

func (db *DB) redisString(ctx context.Context, key string) ([]byte, bool, error) {
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
	data, found, err := db.engine.Get(ctx, valueKey(key))
	if err != nil || !found {
		return nil, false, err
	}
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, false, err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		_, _ = db.redisDeleteRaw(ctx, key)
		return nil, false, nil
	}
	return append([]byte(nil), value.payload...), true, nil
}

func (db *DB) redisWriteString(ctx context.Context, key string, value []byte, expiresAt int64) error {
	if expiresAt <= 0 {
		if err := db.putPayload(ctx, valueKey(key), BytesCodec{}.Name(), value, 0); err != nil {
			return err
		}
		// Strings keep expiry in their envelope; collection metadata must not
		// linger when a key changes type or is recreated.
		return db.engine.Delete(ctx, redisTTLKey(key))
	}
	duration := time.Unix(0, expiresAt).Sub(db.cfg.now())
	if duration <= 0 {
		_, err := db.redisDeleteRaw(ctx, key)
		return err
	}
	return db.putPayload(ctx, valueKey(key), BytesCodec{}.Name(), value, duration)
}

func (db *DB) redisExpiry(ctx context.Context, key string) (int64, bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil || typ == redisTypeNone {
		return 0, false, err
	}
	if typ == redisTypeString {
		data, found, err := db.engine.Get(ctx, valueKey(key))
		if err != nil || !found {
			return 0, false, err
		}
		value, err := unmarshalEnvelope(data)
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

func (db *DB) redisSetExpiry(ctx context.Context, key string, expiresAt int64) (bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil || typ == redisTypeNone {
		return false, err
	}
	if expiresAt <= db.cfg.now().UnixNano() {
		_, err := db.redisDeleteRaw(ctx, key)
		return err == nil, err
	}
	if typ == redisTypeString {
		data, found, err := db.engine.Get(ctx, valueKey(key))
		if err != nil || !found {
			return false, err
		}
		value, err := unmarshalEnvelope(data)
		if err != nil {
			return false, err
		}
		if err := db.redisWriteStringWithCodec(ctx, key, value.payload, value.codec, expiresAt); err != nil {
			return false, err
		}
		_ = db.engine.Delete(ctx, redisTTLKey(key))
		return true, nil
	}
	if err := db.engine.Put(ctx, redisTTLKey(key), encodeRedisExpiry(expiresAt)); err != nil {
		return false, err
	}
	return true, nil
}

func (db *DB) redisWriteStringWithCodec(ctx context.Context, key string, payload []byte, codec string, expiresAt int64) error {
	if expiresAt <= 0 {
		if err := db.putPayload(ctx, valueKey(key), codec, payload, 0); err != nil {
			return err
		}
		return db.engine.Delete(ctx, redisTTLKey(key))
	}
	duration := time.Unix(0, expiresAt).Sub(db.cfg.now())
	if duration <= 0 {
		_, err := db.redisDeleteRaw(ctx, key)
		return err
	}
	return db.putPayload(ctx, valueKey(key), codec, payload, duration)
}

func (db *DB) redisPersist(ctx context.Context, key string) (bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil || typ == redisTypeNone {
		return false, err
	}
	_, found, err := db.redisExpiry(ctx, key)
	if err != nil || !found {
		return false, err
	}
	if typ == redisTypeString {
		data, exists, err := db.engine.Get(ctx, valueKey(key))
		if err != nil || !exists {
			return false, err
		}
		value, err := unmarshalEnvelope(data)
		if err != nil {
			return false, err
		}
		if err := db.putPayload(ctx, valueKey(key), value.codec, value.payload, 0); err != nil {
			return false, err
		}
	} else if err := db.engine.Delete(ctx, redisTTLKey(key)); err != nil {
		return false, err
	}
	return true, nil
}

func (db *DB) redisHashField(ctx context.Context, key, field string) ([]byte, bool, error) {
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
	storageKey := namespacedKey(kindHash, key, field)
	data, found, err := db.engine.Get(ctx, storageKey)
	if err != nil || !found {
		return nil, false, err
	}
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, false, err
	}
	if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
		_ = db.engine.Delete(ctx, storageKey)
		return nil, false, nil
	}
	return append([]byte(nil), value.payload...), true, nil
}

func (db *DB) redisList(ctx context.Context, key string) ([][]byte, bool, error) {
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
	data, found, err := db.engine.Get(ctx, listKey(key))
	if err != nil || !found {
		return nil, false, err
	}
	items, err := decodeList(data)
	if err != nil {
		return nil, false, err
	}
	return items, true, nil
}

func redisItemPayload(data []byte, now time.Time) ([]byte, bool, error) {
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return nil, false, err
	}
	if value.expiresAt > 0 && now.UnixNano() >= value.expiresAt {
		return nil, false, nil
	}
	return append([]byte(nil), value.payload...), true, nil
}

func (db *DB) redisWriteList(ctx context.Context, key string, items [][]byte) error {
	if len(items) == 0 {
		_, err := db.redisDeleteRaw(ctx, key)
		return err
	}
	return db.engine.Put(ctx, listKey(key), encodeList(items))
}
