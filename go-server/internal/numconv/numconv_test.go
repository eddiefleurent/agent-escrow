package numconv

import (
	"math"
	"strings"
	"testing"
)

func TestUint64ToInt64(t *testing.T) {
	t.Parallel()

	v, err := Uint64ToInt64(42, "amount")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}

	_, err = Uint64ToInt64(math.MaxInt64+1, "amount")
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestInt64ToUint64(t *testing.T) {
	t.Parallel()

	v, err := Int64ToUint64(7, "amount")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if v != 7 {
		t.Fatalf("expected 7, got %d", v)
	}

	_, err = Int64ToUint64(-1, "amount")
	if err == nil {
		t.Fatal("expected negative-value error")
	}
}

func TestIntToUint8(t *testing.T) {
	t.Parallel()

	v, err := IntToUint8(255, "tier")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if v != 255 {
		t.Fatalf("expected 255, got %d", v)
	}

	_, err = IntToUint8(256, "tier")
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestUintToInt(t *testing.T) {
	t.Parallel()

	v, err := UintToInt(123, "count")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if v != 123 {
		t.Fatalf("expected 123, got %d", v)
	}

	tooLarge := uint(math.MaxInt) + 1
	_, err = UintToInt(tooLarge, "count")
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestParseOptionalBytes32Hex(t *testing.T) {
	t.Parallel()

	zero, err := ParseOptionalBytes32Hex("")
	if err != nil {
		t.Fatalf("expected no error for empty input, got %v", err)
	}
	var wantZero [32]byte
	if zero != wantZero {
		t.Fatalf("expected zero bytes32, got %#v", zero)
	}

	_, err = ParseOptionalBytes32Hex("abcd")
	if err == nil || !strings.Contains(err.Error(), "0x-prefixed") {
		t.Fatalf("expected prefix error, got %v", err)
	}

	_, err = ParseOptionalBytes32Hex("0x1234")
	if err == nil || !strings.Contains(err.Error(), "32-byte hex") {
		t.Fatalf("expected length error, got %v", err)
	}

	_, err = ParseOptionalBytes32Hex("0x" + strings.Repeat("gg", 32))
	if err == nil {
		t.Fatal("expected invalid hex error")
	}

	valid := "0x" + strings.Repeat("ab", 32)
	got, err := ParseOptionalBytes32Hex(valid)
	if err != nil {
		t.Fatalf("expected no error for valid bytes32, got %v", err)
	}
	for i, b := range got {
		if b != 0xab {
			t.Fatalf("byte %d: expected 0xab, got 0x%x", i, b)
		}
	}
}
