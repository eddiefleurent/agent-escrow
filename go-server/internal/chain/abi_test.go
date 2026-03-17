package chain

import (
	"strings"
	"testing"
)

func TestParseABI(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"abi":[{"inputs":[],"name":"ping","outputs":[],"stateMutability":"nonpayable","type":"function"}]}`)
	parsed, err := parseABI(raw)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := parsed.Methods["ping"]; !ok {
		t.Fatal("expected ping method to exist in parsed ABI")
	}
}

func TestParseABIInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := parseABI([]byte("{"))
	if err == nil || !strings.Contains(err.Error(), "unmarshal artifact") {
		t.Fatalf("expected unmarshal error, got %v", err)
	}
}

func TestParseABIInvalidABIPayload(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"abi":"not-json-array"}`)
	_, err := parseABI(raw)
	if err == nil || !strings.Contains(err.Error(), "parse abi") {
		t.Fatalf("expected parse abi error, got %v", err)
	}
}

func TestEmbeddedABIsLoaded(t *testing.T) {
	t.Parallel()

	if _, ok := FactoryABI.Methods["createEscrow"]; !ok {
		t.Fatal("expected createEscrow in FactoryABI")
	}
	if len(EscrowABI.Methods) == 0 {
		t.Fatal("expected EscrowABI to have methods")
	}
	if _, ok := ERC20ABI.Methods["approve"]; !ok {
		t.Fatal("expected approve in ERC20ABI")
	}
}
