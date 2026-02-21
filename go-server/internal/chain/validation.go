package chain

import (
	"fmt"
	"math/big"
)

// ValidateComplexityFloor checks whether amount meets the configured
// complexity floor. An empty or "0" floorStr disables the check.
func ValidateComplexityFloor(amount *big.Int, floorStr string) error {
	if floorStr == "" {
		return nil
	}
	floor, ok := new(big.Int).SetString(floorStr, 10)
	if !ok || floor.Sign() <= 0 {
		return nil
	}
	if amount == nil {
		return fmt.Errorf("amount is nil")
	}
	if amount.Cmp(floor) < 0 {
		return fmt.Errorf("amount %s is below complexity floor %s", amount, floor)
	}
	return nil
}
