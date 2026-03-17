package main

import (
	"encoding/hex"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestResolveFactoryAddress(t *testing.T) {
	t.Parallel()

	flagAddr := "0x1111111111111111111111111111111111111111"
	envAddr := "0x2222222222222222222222222222222222222222"

	got, err := resolveFactoryAddress(flagAddr, envAddr)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != common.HexToAddress(flagAddr) {
		t.Fatalf("expected flag address precedence, got %s", got.Hex())
	}

	got, err = resolveFactoryAddress("", envAddr)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != common.HexToAddress(envAddr) {
		t.Fatalf("expected env address fallback, got %s", got.Hex())
	}

	got, err = resolveFactoryAddress("", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != common.HexToAddress(defaultFactoryAddress) {
		t.Fatalf("expected default address fallback, got %s", got.Hex())
	}

	if _, err = resolveFactoryAddress("not-an-address", ""); err == nil {
		t.Fatal("expected invalid address error")
	}
}

func TestBuildSelectorsIncludesComputedCreateEscrowSignature(t *testing.T) {
	t.Parallel()

	selectors := buildSelectors()
	const sig = "createEscrow((address,address,address,address,uint256,uint256,uint64,uint64,uint64,bytes32,uint64,address,address,uint64,(uint256,uint64)[]))"
	computed := hex.EncodeToString(crypto.Keccak256([]byte(sig))[:4])

	got, ok := selectors[computed]
	if !ok {
		t.Fatalf("expected computed selector %s to exist", computed)
	}
	if got != sig {
		t.Fatalf("expected selector to map to signature, got %q", got)
	}
}

func TestSelectorPattern(t *testing.T) {
	t.Parallel()

	pattern, err := selectorPattern("c229b1e9")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pattern) != 5 {
		t.Fatalf("expected 5-byte pattern, got %d", len(pattern))
	}
	if pattern[0] != 0x63 {
		t.Fatalf("expected PUSH4 opcode prefix, got 0x%x", pattern[0])
	}

	if _, err := selectorPattern("xyz"); err == nil {
		t.Fatal("expected invalid hex selector error")
	}
}
