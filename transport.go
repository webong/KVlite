package kvlite

import (
	"context"
	"time"
)

// TransportStore is the low-level logical-record view used by optional
// protocol extensions. Its keys and values are KVLite records, not an
// underlying driver's on-disk format. Regular applications should use DB's
// typed APIs instead.
//
// TransportStore deliberately has no Close method: the owning DB keeps sole
// control of the embedded engine's lifetime.
type TransportStore interface {
	Get(context.Context, []byte) ([]byte, bool, error)
	Put(context.Context, []byte, []byte) error
	Delete(context.Context, []byte) error
	ScanPrefix(context.Context, []byte, func(key, value []byte) error) error
	PushList(context.Context, []byte, [][]byte, bool) (int, error)
}

// Transport returns the DB's protocol-extension record store. It lets an
// optional transport preserve KVLite envelopes without making that transport a
// dependency of the embeddable core.
func (db *DB) Transport() TransportStore {
	return dbTransport{db: db}
}

// ProtocolStore is the richer KVLite record view intended for optional
// protocol implementations. It preserves core record and collection layout
// without coupling an embedded application to any network listener or wire
// protocol.
//
// Lock and Unlock bracket one multi-step protocol command. Implementations
// must always release the lock before returning control to their caller.
type ProtocolStore interface {
	TransportStore
	Lock()
	Unlock()
	Now() time.Time
	DeleteLogicalKey(context.Context, string) (bool, error)
	ValueKey(string) []byte
	HashKey(name, field string) []byte
	HashPrefix(name string) []byte
	SetKey(name, member string) []byte
	SetPrefix(name string) []byte
	ListKey(name string) []byte
	CollectionTTLKey(name string) []byte
	LogicalKey(storageKey []byte) (string, bool)
	EncodeRecord(codec string, payload []byte, expiresAt int64) ([]byte, error)
	DecodeRecord(data []byte) (StoredValue, error)
	EncodeList(items [][]byte) []byte
	DecodeList(data []byte) ([][]byte, error)
}

// Protocol returns the KVLite storage layout contract used by optional
// protocol extensions. Normal embedded applications should use DB's typed
// APIs instead.
func (db *DB) Protocol() ProtocolStore {
	return dbProtocol{db: db}
}

type dbTransport struct {
	db *DB
}

func (transport dbTransport) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if err := transport.db.ensureOpen(); err != nil {
		return nil, false, err
	}
	return transport.db.engine.Get(ctx, key)
}

func (transport dbTransport) Put(ctx context.Context, key, value []byte) error {
	if err := transport.db.ensureOpen(); err != nil {
		return err
	}
	return transport.db.engine.Put(ctx, key, value)
}

func (transport dbTransport) Delete(ctx context.Context, key []byte) error {
	if err := transport.db.ensureOpen(); err != nil {
		return err
	}
	return transport.db.engine.Delete(ctx, key)
}

func (transport dbTransport) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	if err := transport.db.ensureOpen(); err != nil {
		return err
	}
	return transport.db.engine.ScanPrefix(ctx, prefix, callback)
}

func (transport dbTransport) PushList(ctx context.Context, key []byte, items [][]byte, left bool) (int, error) {
	if err := transport.db.ensureOpen(); err != nil {
		return 0, err
	}
	return transport.db.pushEncodedList(ctx, key, items, left)
}

type dbProtocol struct {
	db *DB
}

func (store dbProtocol) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	return dbTransport{db: store.db}.Get(ctx, key)
}

func (store dbProtocol) Put(ctx context.Context, key, value []byte) error {
	return dbTransport{db: store.db}.Put(ctx, key, value)
}

func (store dbProtocol) Delete(ctx context.Context, key []byte) error {
	return dbTransport{db: store.db}.Delete(ctx, key)
}

func (store dbProtocol) ScanPrefix(ctx context.Context, prefix []byte, callback func(key, value []byte) error) error {
	return dbTransport{db: store.db}.ScanPrefix(ctx, prefix, callback)
}

func (store dbProtocol) PushList(ctx context.Context, key []byte, items [][]byte, left bool) (int, error) {
	return dbTransport{db: store.db}.PushList(ctx, key, items, left)
}

func (store dbProtocol) Lock() {
	store.db.protocolMu.Lock()
}

func (store dbProtocol) Unlock() {
	store.db.protocolMu.Unlock()
}

func (store dbProtocol) Now() time.Time {
	return store.db.cfg.now()
}

func (store dbProtocol) DeleteLogicalKey(ctx context.Context, key string) (bool, error) {
	if err := store.db.ensureOpen(); err != nil {
		return false, err
	}
	return store.db.deleteLogicalKey(ctx, key)
}

func (store dbProtocol) ValueKey(key string) []byte {
	return valueKey(key)
}

func (store dbProtocol) HashKey(name, field string) []byte {
	return namespacedKey(kindHash, name, field)
}

func (store dbProtocol) HashPrefix(name string) []byte {
	return namespacePrefix(kindHash, name)
}

func (store dbProtocol) SetKey(name, member string) []byte {
	return namespacedKey(kindSet, name, member)
}

func (store dbProtocol) SetPrefix(name string) []byte {
	return namespacePrefix(kindSet, name)
}

func (store dbProtocol) ListKey(name string) []byte {
	return listKey(name)
}

func (store dbProtocol) CollectionTTLKey(name string) []byte {
	return collectionTTLKey(name)
}

func (store dbProtocol) LogicalKey(storageKey []byte) (string, bool) {
	return logicalKeyFromStorage(storageKey)
}

func (store dbProtocol) EncodeRecord(codec string, payload []byte, expiresAt int64) ([]byte, error) {
	return marshalEnvelope(codec, payload, expiresAt)
}

func (store dbProtocol) DecodeRecord(data []byte) (StoredValue, error) {
	value, err := unmarshalEnvelope(data)
	if err != nil {
		return StoredValue{}, err
	}
	return StoredValue{
		Codec:     value.codec,
		Payload:   append([]byte(nil), value.payload...),
		ExpiresAt: value.expiresAt,
	}, nil
}

func (store dbProtocol) EncodeList(items [][]byte) []byte {
	return encodeList(items)
}

func (store dbProtocol) DecodeList(data []byte) ([][]byte, error) {
	return decodeList(data)
}
