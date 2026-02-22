package numconv

import (
	"fmt"
	"math"
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
