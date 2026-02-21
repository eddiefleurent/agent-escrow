package chain

import (
	"math/big"
	"testing"
)

func TestValidateComplexityFloor(t *testing.T) {
	tests := []struct {
		name     string
		amount   *big.Int
		floorStr string
		wantErr  bool
	}{
		{"empty floor allows any amount", big.NewInt(1), "", false},
		{"zero floor allows any amount", big.NewInt(1), "0", false},
		{"amount above floor", big.NewInt(1000), "500", false},
		{"amount at floor", big.NewInt(500), "500", false},
		{"amount below floor", big.NewInt(499), "500", true},
		{"invalid floor string ignored", big.NewInt(1), "not-a-number", false},
		{"negative floor ignored", big.NewInt(1), "-1", false},
		{"nil amount with active floor", nil, "500", true},
		{"nil amount with empty floor", nil, "", false},
		{"large amount above large floor", new(big.Int).Mul(big.NewInt(1e18), big.NewInt(2)), "1000000000000000000", false},
		{"large amount below large floor", big.NewInt(999999999999999999), "1000000000000000000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateComplexityFloor(tt.amount, tt.floorStr)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}
