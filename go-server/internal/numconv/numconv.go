package numconv

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
)

func Uint64ToInt64(v uint64, field string) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("%s out of range for int64: %d", field, v)
	}
	return int64(v), nil
}

func Int64ToUint64(v int64, field string) (uint64, error) {
	if v < 0 {
		return 0, fmt.Errorf("%s cannot be negative: %d", field, v)
	}
	return uint64(v), nil
}

func IntToUint8(v int, field string) (uint8, error) {
	if v < 0 || v > math.MaxUint8 {
		return 0, fmt.Errorf("%s out of range for uint8: %d", field, v)
	}
	return uint8(v), nil
}

func UintToInt(v uint, field string) (int, error) {
	if v > uint(math.MaxInt) {
		return 0, fmt.Errorf("%s out of range for int: %d", field, v)
	}
	return int(v), nil
}

// ParseOptionalBytes32Hex parses an optional 0x-prefixed bytes32 hex string.
// Empty input is accepted and returns the zero [32]byte value.
func ParseOptionalBytes32Hex(raw string) ([32]byte, error) {
	var out [32]byte
	if raw == "" {
		return out, nil
	}
	if !strings.HasPrefix(raw, "0x") {
		return out, errors.New("expected 0x-prefixed hex")
	}

	normalized := raw[2:]
	if len(normalized) != 64 {
		return out, fmt.Errorf("expected 32-byte hex (64 chars), got %d", len(normalized))
	}

	b, err := hex.DecodeString(normalized)
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}
