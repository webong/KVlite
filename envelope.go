package kvlite

import (
	"encoding/binary"
	"fmt"
	"time"
)

var envelopeMagic = [4]byte{'K', 'V', 'L', '1'}

const envelopeHeaderSize = 14

type envelope struct {
	expiresAt int64
	codec     string
	payload   []byte
}

func marshalEnvelope(codec string, payload []byte, expiresAt int64) ([]byte, error) {
	if len(codec) > 65535 {
		return nil, fmt.Errorf("%w: codec name is too long", ErrInvalidArgument)
	}
	result := make([]byte, envelopeHeaderSize+len(codec)+len(payload))
	copy(result[:4], envelopeMagic[:])
	binary.BigEndian.PutUint64(result[4:12], uint64(expiresAt))
	binary.BigEndian.PutUint16(result[12:14], uint16(len(codec)))
	copy(result[14:], codec)
	copy(result[14+len(codec):], payload)
	return result, nil
}

func unmarshalEnvelope(data []byte) (envelope, error) {
	if len(data) < envelopeHeaderSize || string(data[:4]) != string(envelopeMagic[:]) {
		return envelope{}, fmt.Errorf("kvlite: invalid value envelope")
	}
	codecLength := int(binary.BigEndian.Uint16(data[12:14]))
	if len(data) < envelopeHeaderSize+codecLength {
		return envelope{}, fmt.Errorf("kvlite: truncated value envelope")
	}
	return envelope{
		expiresAt: int64(binary.BigEndian.Uint64(data[4:12])),
		codec:     string(data[14 : 14+codecLength]),
		payload:   data[14+codecLength:],
	}, nil
}

func envelopeExpired(data []byte, now time.Time) bool {
	value, err := unmarshalEnvelope(data)
	return err == nil && value.expiresAt > 0 && now.UnixNano() >= value.expiresAt
}
