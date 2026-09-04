package kvlite

import (
	"context"
	"errors"
)

// deleteLogicalKey removes every record that can make up one logical KVLite
// key. Scalar writes use it before storing their replacement so a protocol
// extension cannot leave a conflicting collection representation behind.
//
// It deliberately uses exact lookups for scalar, list, and collection-TTL
// records: prefix matching those forms would also match sibling keys such as
// "user:10" while deleting "user:1".
func (db *DB) deleteLogicalKey(ctx context.Context, key string) (bool, error) {
	keys := make([][]byte, 0, 4)
	for _, storageKey := range [][]byte{valueKey(key), listKey(key), collectionTTLKey(key)} {
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
			if errors.Is(err, ErrDriverUnavailable) {
				continue
			}
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
