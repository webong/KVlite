package kvlite

import "encoding/binary"

const (
	kindValue    byte = 1
	kindHash     byte = 2
	kindSet      byte = 3
	kindList     byte = 4
	kindRedisTTL byte = 5
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

func redisTTLKey(key string) []byte {
	return append([]byte{kindRedisTTL}, key...)
}
