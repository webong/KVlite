package kvlite

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// HSet stores a typed field in a hash. TTL applies to this field only.
func (db *DB) HSet(ctx context.Context, name, field string, value any, options ...PutOption) error {
	if name == "" || field == "" {
		return fmt.Errorf("%w: hash name and field are required", ErrInvalidArgument)
	}
	return db.put(ctx, namespacedKey(kindHash, name, field), value, options...)
}

// HGet decodes one hash field into target.
func (db *DB) HGet(ctx context.Context, name, field string, target any) error {
	return db.get(ctx, namespacedKey(kindHash, name, field), target)
}

// HDelete deletes one or more fields and returns the number that existed.
func (db *DB) HDelete(ctx context.Context, name string, fields ...string) (int, error) {
	if err := db.ensureOpen(); err != nil {
		return 0, err
	}
	deleted := 0
	for _, field := range fields {
		key := namespacedKey(kindHash, name, field)
		_, found, err := db.engine.Get(ctx, key)
		if err != nil {
			return deleted, err
		}
		if !found {
			continue
		}
		if err := db.engine.Delete(ctx, key); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// HGetAll decodes all live fields into a pointer to map[string]T.
func (db *DB) HGetAll(ctx context.Context, name string, target any) error {
	if err := db.ensureOpen(); err != nil {
		return err
	}
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Pointer || targetValue.IsNil() || targetValue.Elem().Kind() != reflect.Map || targetValue.Elem().Type().Key().Kind() != reflect.String {
		return fmt.Errorf("%w: HGetAll target must be a non-nil pointer to map[string]T", ErrInvalidArgument)
	}
	mapType := targetValue.Elem().Type()
	result := reflect.MakeMap(mapType)
	prefix := namespacePrefix(kindHash, name)
	var expired [][]byte
	err := db.engine.ScanPrefix(ctx, prefix, func(key, data []byte) error {
		value := reflect.New(mapType.Elem())
		if err := db.decode(data, value.Interface()); err != nil {
			if errors.Is(err, ErrNotFound) {
				expired = append(expired, append([]byte(nil), key...))
				return nil
			}
			return err
		}
		field := string(key[len(prefix):])
		result.SetMapIndex(reflect.ValueOf(field), value.Elem())
		return nil
	})
	if err != nil {
		return err
	}
	for _, key := range expired {
		_ = db.engine.Delete(ctx, key)
	}
	targetValue.Elem().Set(result)
	return nil
}

// SAdd adds string members to a set and returns the number newly added.
func (db *DB) SAdd(ctx context.Context, name string, members ...string) (int, error) {
	if err := db.ensureOpen(); err != nil {
		return 0, err
	}
	added := 0
	for _, member := range members {
		key := namespacedKey(kindSet, name, member)
		_, found, err := db.engine.Get(ctx, key)
		if err != nil {
			return added, err
		}
		if found {
			continue
		}
		if err := db.engine.Put(ctx, key, []byte{1}); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// SRemove removes members from a set and returns the number removed.
func (db *DB) SRemove(ctx context.Context, name string, members ...string) (int, error) {
	if err := db.ensureOpen(); err != nil {
		return 0, err
	}
	removed := 0
	for _, member := range members {
		key := namespacedKey(kindSet, name, member)
		_, found, err := db.engine.Get(ctx, key)
		if err != nil {
			return removed, err
		}
		if !found {
			continue
		}
		if err := db.engine.Delete(ctx, key); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// SContains reports whether a set contains member.
func (db *DB) SContains(ctx context.Context, name, member string) (bool, error) {
	if err := db.ensureOpen(); err != nil {
		return false, err
	}
	_, found, err := db.engine.Get(ctx, namespacedKey(kindSet, name, member))
	return found, err
}

// SMembers returns set members in stable lexical order.
func (db *DB) SMembers(ctx context.Context, name string) ([]string, error) {
	if err := db.ensureOpen(); err != nil {
		return nil, err
	}
	prefix := namespacePrefix(kindSet, name)
	var members []string
	err := db.engine.ScanPrefix(ctx, prefix, func(key, _ []byte) error {
		members = append(members, string(key[len(prefix):]))
		return nil
	})
	sort.Strings(members)
	return members, err
}

// RPush appends typed values to a list and returns its new length.
func (db *DB) RPush(ctx context.Context, name string, values ...any) (int, error) {
	return db.push(ctx, name, false, values...)
}

// LPush prepends typed values to a list and returns its new length.
func (db *DB) LPush(ctx context.Context, name string, values ...any) (int, error) {
	return db.push(ctx, name, true, values...)
}

func (db *DB) push(ctx context.Context, name string, left bool, values ...any) (int, error) {
	if err := db.ensureOpen(); err != nil {
		return 0, err
	}
	key := listKey(name)
	encoded := make([][]byte, 0, len(values))
	for _, value := range values {
		payload, err := db.cfg.codec.Marshal(value)
		if err != nil {
			return 0, err
		}
		item, err := marshalEnvelope(db.cfg.codec.Name(), payload, 0)
		if err != nil {
			return 0, err
		}
		encoded = append(encoded, item)
	}
	if remote, ok := db.engine.(interface {
		PushList(context.Context, []byte, [][]byte, bool) (int, error)
	}); ok {
		length, err := remote.PushList(ctx, key, encoded, left)
		if !errors.Is(err, errAtomicListPushUnsupported) {
			return length, err
		}
	}
	db.collectionMu.Lock()
	defer db.collectionMu.Unlock()
	items, err := db.readList(ctx, key)
	if err != nil {
		return 0, err
	}
	if left {
		items = append(encoded, items...)
	} else {
		items = append(items, encoded...)
	}
	if err := db.writeList(ctx, key, items); err != nil {
		return 0, err
	}
	return len(items), nil
}

// LRange decodes the inclusive [start, stop] range into a pointer to []T.
// Negative indexes count from the end, matching Redis conventions.
func (db *DB) LRange(ctx context.Context, name string, start, stop int, target any) error {
	if err := db.ensureOpen(); err != nil {
		return err
	}
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Pointer || targetValue.IsNil() || targetValue.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("%w: LRange target must be a non-nil pointer to []T", ErrInvalidArgument)
	}
	items, err := db.readList(ctx, listKey(name))
	if err != nil {
		return err
	}
	start, stop, ok := normalizeRange(len(items), start, stop)
	result := reflect.MakeSlice(targetValue.Elem().Type(), 0, len(items))
	if ok {
		for _, item := range items[start : stop+1] {
			value := reflect.New(targetValue.Elem().Type().Elem())
			if err := db.decode(item, value.Interface()); err != nil {
				return err
			}
			result = reflect.Append(result, value.Elem())
		}
	}
	targetValue.Elem().Set(result)
	return nil
}

func normalizeRange(length, start, stop int) (int, int, bool) {
	if start < 0 {
		start = length + start
	}
	if stop < 0 {
		stop = length + stop
	}
	if start < 0 {
		start = 0
	}
	if stop >= length {
		stop = length - 1
	}
	return start, stop, length > 0 && start < length && stop >= start
}

func (db *DB) readList(ctx context.Context, key []byte) ([][]byte, error) {
	data, found, err := db.engine.Get(ctx, key)
	if err != nil || !found {
		return nil, err
	}
	return decodeList(data)
}

func (db *DB) writeList(ctx context.Context, key []byte, items [][]byte) error {
	return db.engine.Put(ctx, key, encodeList(items))
}

func encodeList(items [][]byte) []byte {
	size := 4
	for _, item := range items {
		size += 4 + len(item)
	}
	result := make([]byte, size)
	binary.BigEndian.PutUint32(result[:4], uint32(len(items)))
	offset := 4
	for _, item := range items {
		binary.BigEndian.PutUint32(result[offset:offset+4], uint32(len(item)))
		offset += 4
		copy(result[offset:], item)
		offset += len(item)
	}
	return result
}

func decodeList(data []byte) ([][]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("kvlite: invalid list encoding")
	}
	count := int(binary.BigEndian.Uint32(data[:4]))
	if count > (len(data)-4)/4 {
		return nil, fmt.Errorf("kvlite: invalid list item count")
	}
	items := make([][]byte, 0, count)
	offset := 4
	for i := 0; i < count; i++ {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("kvlite: truncated list encoding")
		}
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if length < 0 || offset+length > len(data) {
			return nil, fmt.Errorf("kvlite: truncated list item")
		}
		items = append(items, append([]byte(nil), data[offset:offset+length]...))
		offset += length
	}
	if offset != len(data) {
		return nil, fmt.Errorf("kvlite: trailing list data")
	}
	return items, nil
}
