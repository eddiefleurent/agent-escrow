package chain

import (
	"fmt"
	"math/big"
)

// ValidateComplexityFloor checks whether amount meets the configured
// complexity floor. An empty floorStr or the exact value "0" disables the
// check. Any other string that fails to parse as a decimal integer, or that
// parses to a negative value, returns an error.
func ValidateComplexityFloor(amount *big.Int, floorStr string) error {
	if floorStr == "" || floorStr == "0" {
		return nil
	}
	floor, ok := new(big.Int).SetString(floorStr, 10)
	if !ok {
		return fmt.Errorf("invalid complexity floor %q: must be a non-negative integer", floorStr)
	}
	if floor.Sign() < 0 {
		return fmt.Errorf("invalid complexity floor %q: must be non-negative", floorStr)
	}
	if amount == nil {
		return fmt.Errorf("amount is nil")
	}
	if amount.Cmp(floor) < 0 {
		return fmt.Errorf("amount %s is below complexity floor %s", amount, floor)
	}
	return nil
}
