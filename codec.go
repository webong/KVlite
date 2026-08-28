package kvlite

import (
	"encoding/json"
	"fmt"
)

// Codec translates Go values to and from bytes stored by KVLite.
type Codec interface {
	Name() string
	Marshal(value any) ([]byte, error)
	Unmarshal(data []byte, target any) error
}

// JSONCodec is KVLite's portable, zero-configuration default codec.
type JSONCodec struct{}

func (JSONCodec) Name() string { return "json" }

func (JSONCodec) Marshal(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("kvlite: encode json: %w", err)
	}
	return data, nil
}

func (JSONCodec) Unmarshal(data []byte, target any) error {
	if target == nil {
		return fmt.Errorf("%w: decode target cannot be nil", ErrInvalidArgument)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("kvlite: decode json: %w", err)
	}
	return nil
}

// BytesCodec stores byte slices without an additional JSON representation.
// It is useful for language bindings that already serialize values (for
// example, a Python or Rust client sending JSON bytes through the C ABI).
type BytesCodec struct{}

func (BytesCodec) Name() string { return "bytes" }

func (BytesCodec) Marshal(value any) ([]byte, error) {
	data, ok := value.([]byte)
	if !ok {
		return nil, fmt.Errorf("%w: BytesCodec expects []byte, got %T", ErrInvalidArgument, value)
	}
	return append([]byte(nil), data...), nil
}

func (BytesCodec) Unmarshal(data []byte, target any) error {
	result, ok := target.(*[]byte)
	if !ok || result == nil {
		return fmt.Errorf("%w: BytesCodec target must be *[]byte", ErrInvalidArgument)
	}
	*result = append((*result)[:0], data...)
	return nil
}
