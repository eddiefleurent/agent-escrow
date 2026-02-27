package attestation

import (
	"crypto/ecdsa"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

func signAttestation(t *testing.T, key *ecdsa.PrivateKey, a *CompletionAttestation) {
	t.Helper()
	msg := CanonicalCompletionMessage(a)
	hash := crypto.Keccak256Hash([]byte(msg))
	sig, err := crypto.Sign(hash.Bytes(), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	a.Signature = "0x" + hex.EncodeToString(sig)
}

func TestCanonicalCompletionMessage_Deterministic(t *testing.T) {
	childID := int64(42)
	a := &CompletionAttestation{
		Profile:       CompletionAttestationV1,
		LinkID:        "link-1",
		ParentLinkID:  "",
		FromAddress:   "0xAb5801a7D398351b8bE11C439e05C5B3259aeC9B",
		ToAddress:     "0xDEADBEEF00000000000000000000000000000001",
		ChildEscrowID: &childID,
		TaskSpecHash:  "0xabc123",
		OutcomeHash:   "0xdef456",
		IssuedAt:      1700000000,
		ExpiresAt:     1800000000,
		Nonce:         "nonce-1",
	}
	msg1 := CanonicalCompletionMessage(a)
	msg2 := CanonicalCompletionMessage(a)
	if msg1 != msg2 {
		t.Fatalf("non-deterministic: %q != %q", msg1, msg2)
	}
	if msg1 == "" {
		t.Fatal("empty canonical message")
	}
}

func TestVerifyCompletionSignature_Valid(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	a := &CompletionAttestation{
		Profile:     CompletionAttestationV1,
		LinkID:      "link-1",
		FromAddress: addr,
		ToAddress:   "0x0000000000000000000000000000000000000001",
		IssuedAt:    time.Now().Unix() - 60,
		ExpiresAt:   time.Now().Unix() + 3600,
		Nonce:       "nonce-1",
	}
	signAttestation(t, key, a)

	if err := VerifyCompletionSignature(a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyCompletionSignature_WrongSigner(t *testing.T) {
	key1, _ := crypto.GenerateKey()
	key2, _ := crypto.GenerateKey()
	addr2 := crypto.PubkeyToAddress(key2.PublicKey).Hex()

	a := &CompletionAttestation{
		Profile:     CompletionAttestationV1,
		LinkID:      "link-1",
		FromAddress: addr2,
		ToAddress:   "0x0000000000000000000000000000000000000001",
		IssuedAt:    time.Now().Unix() - 60,
		ExpiresAt:   time.Now().Unix() + 3600,
		Nonce:       "nonce-1",
	}
	signAttestation(t, key1, a) // signed by key1 but from_address is key2

	err := VerifyCompletionSignature(a)
	if err == nil {
		t.Fatal("expected signature mismatch error")
	}
}

func TestValidateCompletionAttestation_Expired(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	a := &CompletionAttestation{
		Profile:     CompletionAttestationV1,
		LinkID:      "link-1",
		FromAddress: addr,
		ToAddress:   "0x0000000000000000000000000000000000000001",
		IssuedAt:    time.Now().Unix() - 7200,
		ExpiresAt:   time.Now().Unix() - 3600,
		Nonce:       "nonce-1",
	}
	signAttestation(t, key, a)

	err := ValidateCompletionAttestation(a, time.Now())
	if err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestValidateChain_ValidLinearChain(t *testing.T) {
	key1, _ := crypto.GenerateKey()
	key2, _ := crypto.GenerateKey()
	addr1 := crypto.PubkeyToAddress(key1.PublicKey).Hex()
	addr2 := crypto.PubkeyToAddress(key2.PublicKey).Hex()

	now := time.Now()
	childID := int64(10)
	a1 := CompletionAttestation{
		Profile:       CompletionAttestationV1,
		LinkID:        "link-root",
		FromAddress:   addr1,
		ToAddress:     addr2,
		ChildEscrowID: &childID,
		IssuedAt:      now.Unix() - 60,
		ExpiresAt:     now.Unix() + 3600,
		Nonce:         "n1",
	}
	signAttestation(t, key1, &a1)

	a2 := CompletionAttestation{
		Profile:      CompletionAttestationV1,
		LinkID:       "link-child",
		ParentLinkID: "link-root",
		FromAddress:  addr2,
		ToAddress:    "0x0000000000000000000000000000000000000003",
		IssuedAt:     now.Unix() - 30,
		ExpiresAt:    now.Unix() + 3600,
		Nonce:        "n2",
	}
	signAttestation(t, key2, &a2)

	result := ValidateChain([]CompletionAttestation{a1, a2}, []int64{childID}, now)
	if !result.Valid {
		t.Fatalf("expected valid chain, got reasons: %v", result.Reasons)
	}
	if result.RootHash == "" {
		t.Fatal("expected non-empty root hash")
	}
	if len(result.CoveredEscrows) != 1 || result.CoveredEscrows[0] != childID {
		t.Fatalf("unexpected covered escrows: %v", result.CoveredEscrows)
	}
}

func TestValidateChain_DuplicateLinkID(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	now := time.Now()

	a := CompletionAttestation{
		Profile:     CompletionAttestationV1,
		LinkID:      "link-dup",
		FromAddress: addr,
		ToAddress:   "0x0000000000000000000000000000000000000001",
		IssuedAt:    now.Unix() - 60,
		ExpiresAt:   now.Unix() + 3600,
		Nonce:       "n1",
	}
	signAttestation(t, key, &a)

	result := ValidateChain([]CompletionAttestation{a, a}, nil, now)
	if result.Valid {
		t.Fatal("expected duplicate link_id to fail")
	}
}

func TestValidateChain_MissingChildEscrowCoverage(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	now := time.Now()

	childID1 := int64(10)
	a := CompletionAttestation{
		Profile:       CompletionAttestationV1,
		LinkID:        "link-1",
		FromAddress:   addr,
		ToAddress:     "0x0000000000000000000000000000000000000001",
		ChildEscrowID: &childID1,
		IssuedAt:      now.Unix() - 60,
		ExpiresAt:     now.Unix() + 3600,
		Nonce:         "n1",
	}
	signAttestation(t, key, &a)

	result := ValidateChain([]CompletionAttestation{a}, []int64{10, 20}, now)
	if result.Valid {
		t.Fatal("expected missing coverage to fail")
	}
}

func TestValidateChain_UnknownChildEscrowReference(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	now := time.Now()

	unknownChildID := int64(99)
	a := CompletionAttestation{
		Profile:       CompletionAttestationV1,
		LinkID:        "link-unknown-child",
		FromAddress:   addr,
		ToAddress:     "0x0000000000000000000000000000000000000001",
		ChildEscrowID: &unknownChildID,
		IssuedAt:      now.Unix() - 60,
		ExpiresAt:     now.Unix() + 3600,
		Nonce:         "n-unknown",
	}
	signAttestation(t, key, &a)

	result := ValidateChain([]CompletionAttestation{a}, []int64{10, 20}, now)
	if result.Valid {
		t.Fatal("expected unknown child escrow reference to fail")
	}
}

func TestValidateChain_CycleDetection(t *testing.T) {
	key1, _ := crypto.GenerateKey()
	key2, _ := crypto.GenerateKey()
	addr1 := crypto.PubkeyToAddress(key1.PublicKey).Hex()
	addr2 := crypto.PubkeyToAddress(key2.PublicKey).Hex()
	now := time.Now()

	// Create a cycle: link-a -> link-b -> link-a
	a := CompletionAttestation{
		Profile:      CompletionAttestationV1,
		LinkID:       "link-a",
		ParentLinkID: "link-b",
		FromAddress:  addr1,
		ToAddress:    addr2,
		IssuedAt:     now.Unix() - 60,
		ExpiresAt:    now.Unix() + 3600,
		Nonce:        "n1",
	}
	signAttestation(t, key1, &a)

	b := CompletionAttestation{
		Profile:      CompletionAttestationV1,
		LinkID:       "link-b",
		ParentLinkID: "link-a",
		FromAddress:  addr2,
		ToAddress:    addr1,
		IssuedAt:     now.Unix() - 30,
		ExpiresAt:    now.Unix() + 3600,
		Nonce:        "n2",
	}
	signAttestation(t, key2, &b)

	result := ValidateChain([]CompletionAttestation{a, b}, nil, now)
	if result.Valid {
		t.Fatal("expected cycle detection to fail")
	}
}

func TestValidateChain_BranchingTree(t *testing.T) {
	key1, _ := crypto.GenerateKey()
	key2, _ := crypto.GenerateKey()
	key3, _ := crypto.GenerateKey()
	addr1 := crypto.PubkeyToAddress(key1.PublicKey).Hex()
	addr2 := crypto.PubkeyToAddress(key2.PublicKey).Hex()
	addr3 := crypto.PubkeyToAddress(key3.PublicKey).Hex()
	now := time.Now()

	child1 := int64(10)
	child2 := int64(20)

	root := CompletionAttestation{
		Profile:     CompletionAttestationV1,
		LinkID:      "root",
		FromAddress: addr1,
		ToAddress:   addr2,
		IssuedAt:    now.Unix() - 60,
		ExpiresAt:   now.Unix() + 3600,
		Nonce:       "n1",
	}
	signAttestation(t, key1, &root)

	branch1 := CompletionAttestation{
		Profile:       CompletionAttestationV1,
		LinkID:        "branch-1",
		ParentLinkID:  "root",
		FromAddress:   addr2,
		ToAddress:     addr3,
		ChildEscrowID: &child1,
		IssuedAt:      now.Unix() - 30,
		ExpiresAt:     now.Unix() + 3600,
		Nonce:         "n2",
	}
	signAttestation(t, key2, &branch1)

	branch2 := CompletionAttestation{
		Profile:       CompletionAttestationV1,
		LinkID:        "branch-2",
		ParentLinkID:  "root",
		FromAddress:   addr2,
		ToAddress:     addr3,
		ChildEscrowID: &child2,
		IssuedAt:      now.Unix() - 20,
		ExpiresAt:     now.Unix() + 3600,
		Nonce:         "n3",
	}
	signAttestation(t, key2, &branch2)

	result := ValidateChain([]CompletionAttestation{root, branch1, branch2}, []int64{10, 20}, now)
	if !result.Valid {
		t.Fatalf("expected valid branching tree, got reasons: %v", result.Reasons)
	}
	if len(result.CoveredEscrows) != 2 {
		t.Fatalf("expected 2 covered escrows, got %d", len(result.CoveredEscrows))
	}
}

func TestParseCompletionAttestations(t *testing.T) {
	tests := []struct {
		raw     string
		wantLen int
		wantErr bool
	}{
		{"", 0, false},
		{"[]", 0, false},
		{`[{"profile":"completion-attestation-v1","link_id":"x","from_address":"0x0000000000000000000000000000000000000001","to_address":"0x0000000000000000000000000000000000000002","issued_at":1,"expires_at":` + strconv.FormatInt(time.Now().Unix()+3600, 10) + `,"nonce":"n","signature":"0x00"}]`, 1, false},
		{`invalid`, 0, true},
	}
	for _, tt := range tests {
		atts, err := ParseCompletionAttestations(tt.raw)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseCompletionAttestations(%q): err=%v, wantErr=%v", tt.raw, err, tt.wantErr)
		}
		if len(atts) != tt.wantLen {
			t.Errorf("ParseCompletionAttestations(%q): len=%d, want=%d", tt.raw, len(atts), tt.wantLen)
		}
	}
}

func TestValidateChain_EmptyChain(t *testing.T) {
	result := ValidateChain(nil, nil, time.Now())
	if !result.Valid {
		t.Fatal("expected empty chain to be valid")
	}
}
