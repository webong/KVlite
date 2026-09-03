package kvlite

import "encoding/binary"

const (
	kindValue byte = 1
	kindHash  byte = 2
	kindSet   byte = 3
	kindList  byte = 4
	// kindCollectionTTL stores expiry metadata for collection formats whose
	// values do not carry a KVLite envelope. The byte is intentionally kept
	// stable so optional protocol extensions can continue opening existing
	// databases after being split from the core.
	kindCollectionTTL byte = 5
)

func valueKey(key string) []byte {
	return append([]byte{kindValue}, key...)
}

func namespacePrefix(kind byte, namespace string) []byte {
	result := make([]byte, 1+4+len(namespace))
	result[0] = kind
	binary.BigEndian.PutUint32(result[1:5], uint32(len(namespace)))
	copy(result[5:], namespace)
	return result
}

func namespacedKey(kind byte, namespace, member string) []byte {
	return append(namespacePrefix(kind, namespace), member...)
}

func listKey(name string) []byte {
	return append([]byte{kindList}, name...)
}

func collectionTTLKey(key string) []byte {
	return append([]byte{kindCollectionTTL}, key...)
}

func logicalKeyFromStorage(storageKey []byte) (string, bool) {
	if len(storageKey) == 0 {
		return "", false
	}
	switch storageKey[0] {
	case kindValue, kindList, kindCollectionTTL:
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
