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
