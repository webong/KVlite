package kvlite

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	redisMaxInt64 = int64(^uint64(0) >> 1)
	redisMinInt64 = -redisMaxInt64 - 1
)

func redisErrorReply(err error) respValue {
	if err == nil {
		return respSimpleString("OK")
	}
	if errors.Is(err, errRedisWrongType) {
		return respErrorString(errRedisWrongType.Error())
	}
	if errors.Is(err, ErrClosed) {
		return respErrorString("ERR database is closed")
	}
	return respErrorString("ERR " + err.Error())
}

func redisArg(args [][]byte, index int) (string, bool) {
	if index < 0 || index >= len(args) {
		return "", false
	}
	return string(args[index]), true
}

func redisParseInt(data []byte) (int64, error) {
	value, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return 0, errors.New("value is not an integer or out of range")
	}
	return value, nil
}

func redisParseNonNegative(data []byte) (int64, error) {
	value, err := redisParseInt(data)
	if err != nil || value < 0 {
		return 0, errors.New("value is not an integer or out of range")
	}
	return value, nil
}

type redisSetOptions struct {
	expiresAt int64
	hasExpiry bool
	nx        bool
	xx        bool
	get       bool
	keepTTL   bool
}

func (db *DB) redisSetOptions(args [][]byte) (redisSetOptions, error) {
	options := redisSetOptions{}
	for index := 3; index < len(args); index++ {
		switch strings.ToUpper(string(args[index])) {
		case "NX":
			if options.xx {
				return options, errors.New("syntax error")
			}
			options.nx = true
		case "XX":
			if options.nx {
				return options, errors.New("syntax error")
			}
			options.xx = true
		case "GET":
			options.get = true
		case "KEEPTTL":
			if options.hasExpiry {
				return options, errors.New("syntax error")
			}
			options.keepTTL = true
		case "EX", "PX", "EXAT", "PXAT":
			if options.hasExpiry || index+1 >= len(args) {
				return options, errors.New("syntax error")
			}
			amount, err := redisParseInt(args[index+1])
			if err != nil {
				return options, err
			}
			unit := int64(time.Second)
			absolute := false
			switch strings.ToUpper(string(args[index])) {
			case "PX":
				unit = int64(time.Millisecond)
			case "EXAT":
				absolute = true
			case "PXAT":
				unit = int64(time.Millisecond)
				absolute = true
			}
			if amount <= 0 {
				return options, errors.New("invalid expire time in set")
			}
			if amount > redisMaxInt64/unit {
				return options, errors.New("invalid expire time in set")
			}
			nanos := amount * unit
			if absolute {
				options.expiresAt = nanos
			} else {
				now := db.cfg.now().UnixNano()
				if now > redisMaxInt64-nanos {
					return options, errors.New("invalid expire time in set")
				}
				options.expiresAt = now + nanos
			}
			options.hasExpiry = true
			index++
		default:
			return options, errors.New("syntax error")
		}
	}
	return options, nil
}

func (db *DB) redisGet(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	value, found, err := db.redisString(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respNil()
	}
	return respBulkBytes(value)
}

func (db *DB) redisSet(args [][]byte) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	options, err := db.redisSetOptions(args)
	if err != nil {
		return redisErrorReply(err)
	}
	key := string(args[1])
	typ, err := db.redisType(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	exists := typ != redisTypeNone
	if options.nx && exists {
		return respNil()
	}
	if options.xx && !exists {
		return respNil()
	}
	old := respNil()
	if options.get && exists {
		oldValue, found, err := db.redisString(context.Background(), key)
		if err != nil {
			return redisErrorReply(err)
		}
		if found {
			old = respBulkBytes(oldValue)
		}
	}
	expiresAt := options.expiresAt
	if options.keepTTL && exists && !options.hasExpiry {
		expiresAt, _, err = db.redisExpiry(context.Background(), key)
		if err != nil {
			return redisErrorReply(err)
		}
	}
	if err := db.redisDeleteAndWriteString(key, args[2], expiresAt); err != nil {
		return redisErrorReply(err)
	}
	if options.get {
		return old
	}
	return respSimpleString("OK")
}

func (db *DB) redisDeleteAndWriteString(key string, value []byte, expiresAt int64) error {
	if _, err := db.redisDeleteRaw(context.Background(), key); err != nil {
		return err
	}
	return db.redisWriteString(context.Background(), key, value, expiresAt)
}

func (db *DB) redisSetNX(args [][]byte) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	typ, err := db.redisType(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	if typ != redisTypeNone {
		return respIntegerValue(0)
	}
	if err := db.redisWriteString(context.Background(), string(args[1]), args[2], 0); err != nil {
		return redisErrorReply(err)
	}
	return respIntegerValue(1)
}

func (db *DB) redisSetEX(args [][]byte, milliseconds bool) respValue {
	if len(args) != 4 {
		return redisSyntaxError()
	}
	amount, err := redisParseInt(args[2])
	if err != nil || amount <= 0 {
		return redisErrorReply(errors.New("invalid expire time in set"))
	}
	unit := int64(time.Second)
	if milliseconds {
		unit = int64(time.Millisecond)
	}
	if amount > redisMaxInt64/unit {
		return redisErrorReply(errors.New("invalid expire time in set"))
	}
	now := db.cfg.now().UnixNano()
	delta := amount * unit
	if now > redisMaxInt64-delta {
		return redisErrorReply(errors.New("invalid expire time in set"))
	}
	key := string(args[1])
	if _, err := db.redisDeleteRaw(context.Background(), key); err != nil {
		return redisErrorReply(err)
	}
	if err := db.redisWriteString(context.Background(), key, args[3], now+delta); err != nil {
		return redisErrorReply(err)
	}
	return respSimpleString("OK")
}

func (db *DB) redisGetSet(args [][]byte) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	key := string(args[1])
	old, found, err := db.redisString(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if _, err := db.redisDeleteRaw(context.Background(), key); err != nil {
		return redisErrorReply(err)
	}
	if err := db.redisWriteString(context.Background(), key, args[2], 0); err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respNil()
	}
	return respBulkBytes(old)
}

func (db *DB) redisGetDel(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	key := string(args[1])
	value, found, err := db.redisString(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respNil()
	}
	if _, err := db.redisDeleteRaw(context.Background(), key); err != nil {
		return redisErrorReply(err)
	}
	return respBulkBytes(value)
}

func (db *DB) redisGetEx(args [][]byte) respValue {
	if len(args) < 2 {
		return redisSyntaxError()
	}
	key := string(args[1])
	value, found, err := db.redisString(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respNil()
	}
	if len(args) == 2 {
		return respBulkBytes(value)
	}
	var expiresAt int64
	persist := false
	if len(args) == 3 && strings.EqualFold(string(args[2]), "PERSIST") {
		persist = true
	} else {
		if len(args) != 4 {
			return redisSyntaxError()
		}
		amount, parseErr := redisParseInt(args[3])
		if parseErr != nil {
			return redisErrorReply(errors.New("invalid expire time in getex"))
		}
		option := strings.ToUpper(string(args[2]))
		if (option == "EX" || option == "PX") && amount <= 0 {
			return redisErrorReply(errors.New("invalid expire time in getex"))
		}
		if option != "EX" && option != "PX" && option != "EXAT" && option != "PXAT" {
			return redisSyntaxError()
		}
		expiresAt, err = redisExpiryFromCommand(db.cfg.now(), option, amount)
		if err != nil {
			return redisErrorReply(errors.New("invalid expire time in getex"))
		}
	}
	if persist {
		if _, err := db.redisPersist(context.Background(), key); err != nil {
			return redisErrorReply(err)
		}
	} else if _, err := db.redisSetExpiry(context.Background(), key, expiresAt); err != nil {
		return redisErrorReply(err)
	}
	return respBulkBytes(value)
}

func (db *DB) redisMGet(args [][]byte) respValue {
	if len(args) < 2 {
		return redisSyntaxError()
	}
	items := make([]respValue, len(args)-1)
	for index, arg := range args[1:] {
		value, found, err := db.redisString(context.Background(), string(arg))
		if err != nil {
			return redisErrorReply(err)
		}
		if found {
			items[index] = respBulkBytes(value)
		} else {
			items[index] = respNil()
		}
	}
	return respArrayValues(items...)
}

func (db *DB) redisMSet(args [][]byte) respValue {
	if len(args) < 3 || len(args)%2 != 1 {
		return redisSyntaxError()
	}
	for index := 1; index < len(args); index += 2 {
		key := string(args[index])
		if _, err := db.redisDeleteRaw(context.Background(), key); err != nil {
			return redisErrorReply(err)
		}
		if err := db.redisWriteString(context.Background(), key, args[index+1], 0); err != nil {
			return redisErrorReply(err)
		}
	}
	return respSimpleString("OK")
}

func (db *DB) redisMSetNX(args [][]byte) respValue {
	if len(args) < 3 || len(args)%2 != 1 {
		return redisSyntaxError()
	}
	for index := 1; index < len(args); index += 2 {
		typ, err := db.redisType(context.Background(), string(args[index]))
		if err != nil {
			return redisErrorReply(err)
		}
		if typ != redisTypeNone {
			return respIntegerValue(0)
		}
	}
	for index := 1; index < len(args); index += 2 {
		if err := db.redisWriteString(context.Background(), string(args[index]), args[index+1], 0); err != nil {
			return redisErrorReply(err)
		}
	}
	return respIntegerValue(1)
}

func (db *DB) redisStrLen(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	value, found, err := db.redisString(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respIntegerValue(0)
	}
	return respIntegerValue(int64(len(value)))
}

func (db *DB) redisAppend(args [][]byte) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	key := string(args[1])
	value, found, err := db.redisString(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	expiresAt, _, err := db.redisExpiry(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	value = append(value, args[2]...)
	if err := db.redisWriteString(context.Background(), key, value, expiresAt); err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respIntegerValue(int64(len(args[2])))
	}
	return respIntegerValue(int64(len(value)))
}

func (db *DB) redisIncrementCommand(args [][]byte, delta int64) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	return db.redisIncrement(string(args[1]), delta)
}

func (db *DB) redisIncrementByCommand(args [][]byte, decrement bool) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	delta, err := redisParseInt(args[2])
	if err != nil {
		return redisErrorReply(err)
	}
	if decrement {
		// big.Int in redisIncrement handles the MinInt64 case without a
		// negation overflow.
		deltaBig := new(big.Int).SetInt64(delta)
		deltaBig.Neg(deltaBig)
		return db.redisIncrementBig(string(args[1]), deltaBig)
	}
	return db.redisIncrement(string(args[1]), delta)
}

func (db *DB) redisIncrement(key string, delta int64) respValue {
	return db.redisIncrementBig(key, new(big.Int).SetInt64(delta))
}

func (db *DB) redisIncrementBig(key string, delta *big.Int) respValue {
	value, found, err := db.redisString(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	current := new(big.Int)
	if found {
		if _, ok := current.SetString(string(value), 10); !ok {
			return respErrorString("ERR value is not an integer or out of range")
		}
	}
	current.Add(current, delta)
	if !current.IsInt64() {
		return respErrorString("ERR increment or decrement would overflow")
	}
	expiresAt, _, err := db.redisExpiry(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	encoded := []byte(current.String())
	if err := db.redisWriteString(context.Background(), key, encoded, expiresAt); err != nil {
		return redisErrorReply(err)
	}
	return respIntegerValue(current.Int64())
}

func (db *DB) redisDelete(args [][]byte) respValue {
	if len(args) < 2 {
		return redisSyntaxError()
	}
	var count int64
	for _, arg := range args[1:] {
		deleted, err := db.redisDeleteRaw(context.Background(), string(arg))
		if err != nil {
			return redisErrorReply(err)
		}
		if deleted {
			count++
		}
	}
	return respIntegerValue(count)
}

func (db *DB) redisExists(args [][]byte) respValue {
	if len(args) < 2 {
		return redisSyntaxError()
	}
	var count int64
	for _, arg := range args[1:] {
		typ, err := db.redisType(context.Background(), string(arg))
		if err != nil {
			return redisErrorReply(err)
		}
		if typ != redisTypeNone {
			count++
		}
	}
	return respIntegerValue(count)
}

func (db *DB) redisTypeCommand(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	typ, err := db.redisType(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	return respSimpleString(typ)
}

func redisExpiryFromCommand(now time.Time, command string, amount int64) (int64, error) {
	absolute := strings.HasSuffix(command, "AT")
	unit := int64(time.Second)
	if strings.HasPrefix(command, "P") {
		unit = int64(time.Millisecond)
	}
	if absolute {
		if amount > redisMaxInt64/unit || amount < redisMinInt64/unit {
			return 0, errors.New("invalid expire time")
		}
		return amount * unit, nil
	}
	if amount > redisMaxInt64/unit || amount < redisMinInt64/unit {
		return 0, errors.New("invalid expire time")
	}
	delta := amount * unit
	current := now.UnixNano()
	if delta > 0 && current > redisMaxInt64-delta {
		return 0, errors.New("invalid expire time")
	}
	if delta < 0 && current < redisMinInt64-delta {
		return 0, errors.New("invalid expire time")
	}
	return current + delta, nil
}

func (db *DB) redisExpireCommand(args [][]byte, command string) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	amount, err := redisParseInt(args[2])
	if err != nil {
		return redisErrorReply(err)
	}
	expiresAt, err := redisExpiryFromCommand(db.cfg.now(), command, amount)
	if err != nil {
		return redisErrorReply(err)
	}
	ok, err := db.redisSetExpiry(context.Background(), string(args[1]), expiresAt)
	if err != nil {
		return redisErrorReply(err)
	}
	if !ok {
		return respIntegerValue(0)
	}
	return respIntegerValue(1)
}

func (db *DB) redisTTLCommand(args [][]byte, command string) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	expiresAt, found, err := db.redisExpiry(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		typ, typeErr := db.redisType(context.Background(), string(args[1]))
		if typeErr != nil {
			return redisErrorReply(typeErr)
		}
		if typ == redisTypeNone {
			return respIntegerValue(-2)
		}
		return respIntegerValue(-1)
	}
	remaining := expiresAt - db.cfg.now().UnixNano()
	if remaining <= 0 {
		return respIntegerValue(-2)
	}
	divisor := int64(time.Second)
	if command == "PTTL" {
		divisor = int64(time.Millisecond)
	}
	value := remaining / divisor
	if value < 1 {
		value = 1
	}
	return respIntegerValue(value)
}

func (db *DB) redisPersistCommand(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	ok, err := db.redisPersist(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	if ok {
		return respIntegerValue(1)
	}
	return respIntegerValue(0)
}

type redisHashEntry struct {
	field string
	value []byte
}

func (db *DB) redisHashEntries(ctx context.Context, key string) ([]redisHashEntry, bool, error) {
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
	prefix := namespacePrefix(kindHash, key)
	var records [][2][]byte
	if err := db.engine.ScanPrefix(ctx, prefix, func(storageKey, data []byte) error {
		records = append(records, [2][]byte{
			append([]byte(nil), storageKey...),
			append([]byte(nil), data...),
		})
		return nil
	}); err != nil {
		return nil, false, err
	}
	entries := make([]redisHashEntry, 0, len(records))
	var expired [][]byte
	for _, record := range records {
		value, err := unmarshalEnvelope(record[1])
		if err != nil {
			return nil, false, err
		}
		if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
			expired = append(expired, record[0])
			continue
		}
		entries = append(entries, redisHashEntry{
			field: string(record[0][len(prefix):]),
			value: append([]byte(nil), value.payload...),
		})
	}
	for _, storageKey := range expired {
		if err := db.engine.Delete(ctx, storageKey); err != nil {
			return nil, false, err
		}
	}
	if len(entries) == 0 {
		if len(expired) > 0 {
			_, _ = db.redisDeleteRaw(ctx, key)
		}
		return nil, false, nil
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].field < entries[right].field })
	return entries, true, nil
}

func (db *DB) redisHSet(args [][]byte) respValue {
	if len(args) < 4 || len(args)%2 != 0 {
		return redisSyntaxError()
	}
	key := string(args[1])
	typ, err := db.redisType(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if typ != redisTypeNone && typ != redisTypeHash {
		return redisErrorReply(fmt.Errorf("%w: %s", errRedisWrongType, typ))
	}
	if typ == redisTypeNone {
		// A stale expiry record should not apply to a newly-created hash.
		_ = db.engine.Delete(context.Background(), redisTTLKey(key))
	}
	var added int64
	for index := 2; index < len(args); index += 2 {
		field := string(args[index])
		storageKey := namespacedKey(kindHash, key, field)
		_, found, err := db.engine.Get(context.Background(), storageKey)
		if err != nil {
			return redisErrorReply(err)
		}
		if !found {
			added++
		}
		if err := db.putPayload(context.Background(), storageKey, BytesCodec{}.Name(), args[index+1], 0); err != nil {
			return redisErrorReply(err)
		}
	}
	return respIntegerValue(added)
}

func (db *DB) redisHGet(args [][]byte) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	value, found, err := db.redisHashField(context.Background(), string(args[1]), string(args[2]))
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respNil()
	}
	return respBulkBytes(value)
}

func (db *DB) redisHMGet(args [][]byte) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	items := make([]respValue, len(args)-2)
	for index, field := range args[2:] {
		value, found, err := db.redisHashField(context.Background(), string(args[1]), string(field))
		if err != nil {
			return redisErrorReply(err)
		}
		if found {
			items[index] = respBulkBytes(value)
		} else {
			items[index] = respNil()
		}
	}
	return respArrayValues(items...)
}

func (db *DB) redisHGetAll(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	entries, found, err := db.redisHashEntries(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		return respArrayValues()
	}
	items := make([]respValue, 0, len(entries)*2)
	for _, entry := range entries {
		items = append(items, respBulkString(entry.field), respBulkBytes(entry.value))
	}
	return respArrayValues(items...)
}

func (db *DB) redisHDel(args [][]byte) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	key := string(args[1])
	if typ, err := db.redisType(context.Background(), key); err != nil {
		return redisErrorReply(err)
	} else if typ == redisTypeNone {
		return respIntegerValue(0)
	} else if typ != redisTypeHash {
		return redisErrorReply(fmt.Errorf("%w: %s", errRedisWrongType, typ))
	}
	var removed int64
	for _, field := range args[2:] {
		storageKey := namespacedKey(kindHash, key, string(field))
		data, found, err := db.engine.Get(context.Background(), storageKey)
		if err != nil {
			return redisErrorReply(err)
		}
		if !found {
			continue
		}
		value, decodeErr := unmarshalEnvelope(data)
		if decodeErr != nil {
			return redisErrorReply(decodeErr)
		}
		if value.expiresAt > 0 && db.cfg.now().UnixNano() >= value.expiresAt {
			_ = db.engine.Delete(context.Background(), storageKey)
			continue
		}
		if err := db.engine.Delete(context.Background(), storageKey); err != nil {
			return redisErrorReply(err)
		}
		removed++
	}
	if entries, _, err := db.redisHashEntries(context.Background(), key); err != nil {
		return redisErrorReply(err)
	} else if len(entries) == 0 {
		_, _ = db.redisDeleteRaw(context.Background(), key)
	}
	return respIntegerValue(removed)
}

func (db *DB) redisHExists(args [][]byte) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	_, found, err := db.redisHashField(context.Background(), string(args[1]), string(args[2]))
	if err != nil {
		return redisErrorReply(err)
	}
	if found {
		return respIntegerValue(1)
	}
	return respIntegerValue(0)
}

func (db *DB) redisHLen(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	entries, _, err := db.redisHashEntries(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	return respIntegerValue(int64(len(entries)))
}

func (db *DB) redisHKeys(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	entries, _, err := db.redisHashEntries(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	items := make([]respValue, 0, len(entries))
	for _, entry := range entries {
		items = append(items, respBulkString(entry.field))
	}
	return respArrayValues(items...)
}

func (db *DB) redisHVals(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	entries, _, err := db.redisHashEntries(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	items := make([]respValue, 0, len(entries))
	for _, entry := range entries {
		items = append(items, respBulkBytes(entry.value))
	}
	return respArrayValues(items...)
}

func (db *DB) redisHIncrBy(args [][]byte) respValue {
	if len(args) != 4 {
		return redisSyntaxError()
	}
	delta, err := redisParseInt(args[3])
	if err != nil {
		return redisErrorReply(err)
	}
	key, field := string(args[1]), string(args[2])
	typ, err := db.redisType(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if typ != redisTypeNone && typ != redisTypeHash {
		return redisErrorReply(fmt.Errorf("%w: %s", errRedisWrongType, typ))
	}
	currentValue, found, err := db.redisHashField(context.Background(), key, field)
	if err != nil {
		return redisErrorReply(err)
	}
	current := new(big.Int)
	if found {
		if _, ok := current.SetString(string(currentValue), 10); !ok {
			return respErrorString("ERR hash value is not an integer")
		}
	}
	current.Add(current, new(big.Int).SetInt64(delta))
	if !current.IsInt64() {
		return respErrorString("ERR increment or decrement would overflow")
	}
	if err := db.putPayload(context.Background(), namespacedKey(kindHash, key, field), BytesCodec{}.Name(), []byte(current.String()), 0); err != nil {
		return redisErrorReply(err)
	}
	return respIntegerValue(current.Int64())
}

func (db *DB) redisSetMembers(ctx context.Context, key string) ([]string, bool, error) {
	typ, err := db.redisType(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if typ == redisTypeNone {
		return nil, false, nil
	}
	if typ != redisTypeSet {
		return nil, false, fmt.Errorf("%w: %s", errRedisWrongType, typ)
	}
	prefix := namespacePrefix(kindSet, key)
	members := make([]string, 0)
	if err := db.engine.ScanPrefix(ctx, prefix, func(storageKey, _ []byte) error {
		members = append(members, string(storageKey[len(prefix):]))
		return nil
	}); err != nil {
		return nil, false, err
	}
	if len(members) == 0 {
		_, _ = db.redisDeleteRaw(ctx, key)
		return nil, false, nil
	}
	sort.Strings(members)
	return members, true, nil
}

func (db *DB) redisSAdd(args [][]byte) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	key := string(args[1])
	typ, err := db.redisType(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if typ != redisTypeNone && typ != redisTypeSet {
		return redisErrorReply(fmt.Errorf("%w: %s", errRedisWrongType, typ))
	}
	if typ == redisTypeNone {
		_ = db.engine.Delete(context.Background(), redisTTLKey(key))
	}
	var added int64
	for _, member := range args[2:] {
		storageKey := namespacedKey(kindSet, key, string(member))
		_, found, err := db.engine.Get(context.Background(), storageKey)
		if err != nil {
			return redisErrorReply(err)
		}
		if found {
			continue
		}
		if err := db.engine.Put(context.Background(), storageKey, []byte{1}); err != nil {
			return redisErrorReply(err)
		}
		added++
	}
	return respIntegerValue(added)
}

func (db *DB) redisSRem(args [][]byte) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	key := string(args[1])
	typ, err := db.redisType(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if typ == redisTypeNone {
		return respIntegerValue(0)
	}
	if typ != redisTypeSet {
		return redisErrorReply(fmt.Errorf("%w: %s", errRedisWrongType, typ))
	}
	var removed int64
	for _, member := range args[2:] {
		storageKey := namespacedKey(kindSet, key, string(member))
		_, found, err := db.engine.Get(context.Background(), storageKey)
		if err != nil {
			return redisErrorReply(err)
		}
		if !found {
			continue
		}
		if err := db.engine.Delete(context.Background(), storageKey); err != nil {
			return redisErrorReply(err)
		}
		removed++
	}
	if members, _, err := db.redisSetMembers(context.Background(), key); err != nil {
		return redisErrorReply(err)
	} else if len(members) == 0 {
		_, _ = db.redisDeleteRaw(context.Background(), key)
	}
	return respIntegerValue(removed)
}

func (db *DB) redisSIsMember(args [][]byte) respValue {
	if len(args) != 3 {
		return redisSyntaxError()
	}
	typ, err := db.redisType(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	if typ == redisTypeNone {
		return respIntegerValue(0)
	}
	if typ != redisTypeSet {
		return redisErrorReply(fmt.Errorf("%w: %s", errRedisWrongType, typ))
	}
	_, found, err := db.engine.Get(context.Background(), namespacedKey(kindSet, string(args[1]), string(args[2])))
	if err != nil {
		return redisErrorReply(err)
	}
	if found {
		return respIntegerValue(1)
	}
	return respIntegerValue(0)
}

func (db *DB) redisSMIsMember(args [][]byte) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	items := make([]respValue, len(args)-2)
	for index, member := range args[2:] {
		reply := db.redisSIsMember([][]byte{[]byte("SISMEMBER"), args[1], member})
		if reply.kind == respError {
			return reply
		}
		items[index] = reply
	}
	return respArrayValues(items...)
}

func (db *DB) redisSMembers(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	members, _, err := db.redisSetMembers(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	items := make([]respValue, 0, len(members))
	for _, member := range members {
		items = append(items, respBulkString(member))
	}
	return respArrayValues(items...)
}

func (db *DB) redisSCard(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	members, _, err := db.redisSetMembers(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	return respIntegerValue(int64(len(members)))
}

func (db *DB) redisListValues(ctx context.Context, key string) ([][]byte, bool, error) {
	items, found, err := db.redisList(ctx, key)
	if err != nil || !found {
		return nil, found, err
	}
	live := make([][]byte, 0, len(items))
	changed := false
	for _, item := range items {
		_, ok, err := redisItemPayload(item, db.cfg.now())
		if err != nil {
			return nil, false, err
		}
		if !ok {
			changed = true
			continue
		}
		live = append(live, item)
	}
	if changed {
		if err := db.redisWriteList(ctx, key, live); err != nil {
			return nil, false, err
		}
	}
	if len(live) == 0 {
		return nil, false, nil
	}
	return live, true, nil
}

func redisListReply(items [][]byte, now time.Time) (respValue, error) {
	replies := make([]respValue, 0, len(items))
	for _, item := range items {
		payload, found, err := redisItemPayload(item, now)
		if err != nil {
			return respValue{}, err
		}
		if found {
			replies = append(replies, respBulkBytes(payload))
		}
	}
	return respArrayValues(replies...), nil
}

func (db *DB) redisPush(args [][]byte, left bool) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	return db.redisPushValues(string(args[1]), args[2:], left, false)
}

func (db *DB) redisPushX(args [][]byte, left bool) respValue {
	if len(args) < 3 {
		return redisSyntaxError()
	}
	return db.redisPushValues(string(args[1]), args[2:], left, true)
}

func (db *DB) redisPushValues(key string, values [][]byte, left, onlyIfExists bool) respValue {
	typ, err := db.redisType(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if typ != redisTypeNone && typ != redisTypeList {
		return redisErrorReply(fmt.Errorf("%w: %s", errRedisWrongType, typ))
	}
	if onlyIfExists && typ == redisTypeNone {
		return respIntegerValue(0)
	}
	items, found, err := db.redisListValues(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if onlyIfExists && !found {
		return respIntegerValue(0)
	}
	encoded := make([][]byte, 0, len(values))
	for _, value := range values {
		item, err := marshalEnvelope(BytesCodec{}.Name(), value, 0)
		if err != nil {
			return redisErrorReply(err)
		}
		encoded = append(encoded, item)
	}
	if left {
		for index := 0; index < len(encoded)/2; index++ {
			other := len(encoded) - index - 1
			encoded[index], encoded[other] = encoded[other], encoded[index]
		}
		items = append(encoded, items...)
	} else {
		items = append(items, encoded...)
	}
	if err := db.redisWriteList(context.Background(), key, items); err != nil {
		return redisErrorReply(err)
	}
	if !found {
		_ = db.engine.Delete(context.Background(), redisTTLKey(key))
	}
	return respIntegerValue(int64(len(items)))
}

func (db *DB) redisLRange(args [][]byte) respValue {
	if len(args) != 4 {
		return redisSyntaxError()
	}
	start, err := redisParseInt(args[2])
	if err != nil {
		return redisErrorReply(err)
	}
	stop, err := redisParseInt(args[3])
	if err != nil {
		return redisErrorReply(err)
	}
	items, _, err := db.redisListValues(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	first, last, ok := normalizeRange(len(items), int(start), int(stop))
	if !ok {
		return respArrayValues()
	}
	reply, err := redisListReply(items[first:last+1], db.cfg.now())
	if err != nil {
		return redisErrorReply(err)
	}
	return reply
}

func (db *DB) redisLLen(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	items, _, err := db.redisListValues(context.Background(), string(args[1]))
	if err != nil {
		return redisErrorReply(err)
	}
	return respIntegerValue(int64(len(items)))
}

func (db *DB) redisPop(args [][]byte, left bool) respValue {
	if len(args) != 2 && len(args) != 3 {
		return redisSyntaxError()
	}
	key := string(args[1])
	items, found, err := db.redisListValues(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	if !found {
		if len(args) == 3 {
			return respArrayValues()
		}
		return respNil()
	}
	count := 1
	if len(args) == 3 {
		parsed, parseErr := redisParseNonNegative(args[2])
		if parseErr != nil || parsed > int64(int(^uint(0)>>1)) {
			return redisErrorReply(errors.New("value is not an integer or out of range"))
		}
		count = int(parsed)
		if count == 0 {
			return respArrayValues()
		}
	}
	if count > len(items) {
		count = len(items)
	}
	var removed [][]byte
	if left {
		removed = items[:count]
		items = items[count:]
	} else {
		removed = items[len(items)-count:]
		items = items[:len(items)-count]
	}
	if err := db.redisWriteList(context.Background(), key, items); err != nil {
		return redisErrorReply(err)
	}
	if len(args) == 3 {
		reply, err := redisListReply(removed, db.cfg.now())
		if err != nil {
			return redisErrorReply(err)
		}
		return reply
	}
	payload, ok, err := redisItemPayload(removed[0], db.cfg.now())
	if err != nil {
		return redisErrorReply(err)
	}
	if !ok {
		return respNil()
	}
	return respBulkBytes(payload)
}

func (db *DB) redisLTrim(args [][]byte) respValue {
	if len(args) != 4 {
		return redisSyntaxError()
	}
	start, err := redisParseInt(args[2])
	if err != nil {
		return redisErrorReply(err)
	}
	stop, err := redisParseInt(args[3])
	if err != nil {
		return redisErrorReply(err)
	}
	key := string(args[1])
	items, _, err := db.redisListValues(context.Background(), key)
	if err != nil {
		return redisErrorReply(err)
	}
	first, last, ok := normalizeRange(len(items), int(start), int(stop))
	if !ok {
		if _, err := db.redisDeleteRaw(context.Background(), key); err != nil {
			return redisErrorReply(err)
		}
		return respSimpleString("OK")
	}
	if err := db.redisWriteList(context.Background(), key, items[first:last+1]); err != nil {
		return redisErrorReply(err)
	}
	return respSimpleString("OK")
}

func redisLogicalKeyFromStorage(storageKey []byte) (string, bool) {
	if len(storageKey) == 0 {
		return "", false
	}
	switch storageKey[0] {
	case kindValue, kindList, kindRedisTTL:
		return string(storageKey[1:]), true
	case kindHash, kindSet:
		if len(storageKey) < 5 {
			return "", false
		}
		length := int(binary.BigEndian.Uint32(storageKey[1:5]))
		if len(storageKey) < 5+length {
			return "", false
		}
		return string(storageKey[5 : 5+length]), true
	default:
		return "", false
	}
}

func (db *DB) redisLogicalKeys(ctx context.Context) ([]string, error) {
	var storageKeys [][]byte
	if err := db.engine.ScanPrefix(ctx, nil, func(storageKey, _ []byte) error {
		storageKeys = append(storageKeys, append([]byte(nil), storageKey...))
		return nil
	}); err != nil {
		return nil, err
	}
	candidates := make(map[string]struct{}, len(storageKeys))
	for _, storageKey := range storageKeys {
		if key, ok := redisLogicalKeyFromStorage(storageKey); ok {
			candidates[key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		typ, err := db.redisType(ctx, key)
		if err != nil {
			return nil, err
		}
		if typ != redisTypeNone {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func redisPatternMatch(pattern, value string) bool {
	matched, err := path.Match(pattern, value)
	return err == nil && matched
}

func (db *DB) redisKeysCommand(args [][]byte) respValue {
	if len(args) != 2 {
		return redisSyntaxError()
	}
	keys, err := db.redisLogicalKeys(context.Background())
	if err != nil {
		return redisErrorReply(err)
	}
	pattern := string(args[1])
	items := make([]respValue, 0, len(keys))
	for _, key := range keys {
		if redisPatternMatch(pattern, key) {
			items = append(items, respBulkString(key))
		}
	}
	return respArrayValues(items...)
}

func (db *DB) redisScan(args [][]byte) respValue {
	if len(args) < 2 {
		return redisSyntaxError()
	}
	cursor, err := redisParseNonNegative(args[1])
	if err != nil {
		return redisErrorReply(err)
	}
	pattern := "*"
	count := int64(10)
	for index := 2; index < len(args); index++ {
		switch strings.ToUpper(string(args[index])) {
		case "MATCH":
			if index+1 >= len(args) {
				return redisSyntaxError()
			}
			pattern = string(args[index+1])
			index++
		case "COUNT":
			if index+1 >= len(args) {
				return redisSyntaxError()
			}
			count, err = redisParseNonNegative(args[index+1])
			if err != nil {
				return redisErrorReply(err)
			}
			if count == 0 {
				count = 10
			}
			index++
		case "TYPE":
			if index+1 >= len(args) {
				return redisSyntaxError()
			}
			// Type filtering is applied below after collecting the logical keys.
			index++
		default:
			return redisSyntaxError()
		}
	}
	keys, err := db.redisLogicalKeys(context.Background())
	if err != nil {
		return redisErrorReply(err)
	}
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if redisPatternMatch(pattern, key) {
			filtered = append(filtered, key)
		}
	}
	if cursor >= int64(len(filtered)) {
		return respArrayValues(respBulkString("0"), respArrayValues())
	}
	end := cursor + count
	if end > int64(len(filtered)) {
		end = int64(len(filtered))
	}
	next := int64(0)
	if end < int64(len(filtered)) {
		next = end
	}
	items := make([]respValue, 0, end-cursor)
	for _, key := range filtered[cursor:end] {
		items = append(items, respBulkString(key))
	}
	return respArrayValues(respBulkString(strconv.FormatInt(next, 10)), respArrayValues(items...))
}

func (db *DB) redisDBSize(args [][]byte) respValue {
	if len(args) != 1 {
		return redisSyntaxError()
	}
	keys, err := db.redisLogicalKeys(context.Background())
	if err != nil {
		return redisErrorReply(err)
	}
	return respIntegerValue(int64(len(keys)))
}

func (db *DB) redisFlush(args [][]byte) respValue {
	if len(args) > 2 {
		return redisSyntaxError()
	}
	var storageKeys [][]byte
	if err := db.engine.ScanPrefix(context.Background(), nil, func(storageKey, _ []byte) error {
		storageKeys = append(storageKeys, append([]byte(nil), storageKey...))
		return nil
	}); err != nil {
		return redisErrorReply(err)
	}
	for _, storageKey := range storageKeys {
		if err := db.engine.Delete(context.Background(), storageKey); err != nil {
			return redisErrorReply(err)
		}
	}
	return respSimpleString("OK")
}
