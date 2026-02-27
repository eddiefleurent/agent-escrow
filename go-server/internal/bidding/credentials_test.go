package bidding

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

func makeTestAttestation(t *testing.T) (*Attestation, string) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	issuerAddr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	subjectKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	subjectAddr := crypto.PubkeyToAddress(subjectKey.PublicKey).Hex()

	att := &Attestation{
		Profile:        attestationV1Profile,
		IssuerAddress:  issuerAddr,
		SubjectAddress: subjectAddr,
		Domain:         "code-review",
		Capabilities:   []string{"solidity", "go"},
		IssuedAt:       time.Now().Add(-1 * time.Hour).Unix(),
		ExpiresAt:      time.Now().Add(24 * time.Hour).Unix(),
		Nonce:          "test-nonce-1",
	}

	msg := CanonicalAttestationMessage(att)
	msgHash := crypto.Keccak256Hash([]byte(msg))
	sig, err := crypto.Sign(msgHash.Bytes(), key)
	if err != nil {
		t.Fatal(err)
	}
	att.Signature = "0x" + hex.EncodeToString(sig)

	return att, subjectAddr
}

func TestCanonicalAttestationMessageStability(t *testing.T) {
	att := &Attestation{
		Profile:        attestationV1Profile,
		IssuerAddress:  "0xAb5801a7D398351b8bE11C439e05C5b3259aec9B",
		SubjectAddress: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
		Domain:         "Smart-Contract-Audit",
		Capabilities:   []string{"Solidity", "Go"},
		IssuedAt:       1700000000,
		ExpiresAt:      1700100000,
		Nonce:          "nonce123",
	}
	msg1 := CanonicalAttestationMessage(att)
	msg2 := CanonicalAttestationMessage(att)
	if msg1 != msg2 {
		t.Fatalf("canonical message not stable: %q vs %q", msg1, msg2)
	}

	// Capabilities should be sorted and lowercased.
	att2 := *att
	att2.Capabilities = []string{"Go", "Solidity"}
	if CanonicalAttestationMessage(&att2) != msg1 {
		t.Fatal("canonical message should not depend on capability ordering")
	}
}

func TestVerifyAttestationSignature_Valid(t *testing.T) {
	att, _ := makeTestAttestation(t)
	if err := VerifyAttestationSignature(att); err != nil {
		t.Fatalf("expected valid signature, got: %v", err)
	}
}

func TestVerifyAttestationSignature_WrongIssuer(t *testing.T) {
	att, _ := makeTestAttestation(t)
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	att.IssuerAddress = crypto.PubkeyToAddress(otherKey.PublicKey).Hex()
	err = VerifyAttestationSignature(att)
	if err == nil {
		t.Fatal("expected signature mismatch error")
	}
}

func TestVerifyAttestationSignature_InvalidProfile(t *testing.T) {
	att, _ := makeTestAttestation(t)
	att.Profile = "unknown-profile"
	err := VerifyAttestationSignature(att)
	if err == nil {
		t.Fatal("expected profile error")
	}
}

func TestValidateAttestation_SubjectMismatch(t *testing.T) {
	att, _ := makeTestAttestation(t)
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongBidder := crypto.PubkeyToAddress(otherKey.PublicKey).Hex()
	err = ValidateAttestation(att, wrongBidder, time.Now())
	if err == nil {
		t.Fatal("expected subject mismatch error")
	}
}

func TestValidateAttestation_Expired(t *testing.T) {
	att, bidder := makeTestAttestation(t)
	att.ExpiresAt = time.Now().Add(-1 * time.Hour).Unix()
	err := ValidateAttestation(att, bidder, time.Now())
	if err == nil {
		t.Fatal("expected expiration error")
	}
}

func TestValidateAttestation_FutureIssuedAt(t *testing.T) {
	att, bidder := makeTestAttestation(t)
	att.IssuedAt = time.Now().Add(1 * time.Hour).Unix()
	err := ValidateAttestation(att, bidder, time.Now())
	if err == nil {
		t.Fatal("expected future issued_at error")
	}
}

func TestMatchRequirements_NoRequirements(t *testing.T) {
	result := MatchRequirements(nil, nil)
	if !result.Verified {
		t.Fatal("no requirements should always pass")
	}
}

func TestMatchRequirements_MatchSuccess(t *testing.T) {
	reqs := []CredentialRequirement{
		{Domain: "code-review", Capabilities: []string{"solidity"}},
	}
	atts := []Attestation{
		{Domain: "code-review", Capabilities: []string{"solidity", "go"}, IssuerAddress: "0xAb5801a7D398351b8bE11C439e05C5b3259aec9B"},
	}
	result := MatchRequirements(reqs, atts)
	if !result.Verified {
		t.Fatalf("expected match, got: %+v", result)
	}
}

func TestMatchRequirements_MatchFailure_MissingCapability(t *testing.T) {
	reqs := []CredentialRequirement{
		{Domain: "code-review", Capabilities: []string{"rust"}},
	}
	atts := []Attestation{
		{Domain: "code-review", Capabilities: []string{"solidity", "go"}, IssuerAddress: "0xAb5801a7D398351b8bE11C439e05C5b3259aec9B"},
	}
	result := MatchRequirements(reqs, atts)
	if result.Verified {
		t.Fatal("expected match failure")
	}
	if len(result.Reasons) == 0 {
		t.Fatal("expected reason for failure")
	}
}

func TestMatchRequirements_TrustedIssuerFilter(t *testing.T) {
	trustedIssuer := "0xAb5801a7D398351b8bE11C439e05C5b3259aec9B"
	untrustedIssuer := "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"

	reqs := []CredentialRequirement{
		{Domain: "audit", Capabilities: []string{"security"}, TrustedIssuers: []string{trustedIssuer}},
	}

	attsUntrusted := []Attestation{
		{Domain: "audit", Capabilities: []string{"security"}, IssuerAddress: untrustedIssuer},
	}
	result := MatchRequirements(reqs, attsUntrusted)
	if result.Verified {
		t.Fatal("expected rejection of untrusted issuer")
	}

	attsTrusted := []Attestation{
		{Domain: "audit", Capabilities: []string{"security"}, IssuerAddress: trustedIssuer},
	}
	result = MatchRequirements(reqs, attsTrusted)
	if !result.Verified {
		t.Fatalf("expected acceptance of trusted issuer, got: %+v", result)
	}
}

func TestMatchRequirements_DomainMismatch(t *testing.T) {
	reqs := []CredentialRequirement{
		{Domain: "defi", Capabilities: []string{"lending"}},
	}
	atts := []Attestation{
		{Domain: "code-review", Capabilities: []string{"lending"}, IssuerAddress: "0xAb5801a7D398351b8bE11C439e05C5b3259aec9B"},
	}
	result := MatchRequirements(reqs, atts)
	if result.Verified {
		t.Fatal("expected domain mismatch failure")
	}
}

func TestParseCredentialRequirements_Empty(t *testing.T) {
	reqs, err := ParseCredentialRequirements("")
	if err != nil {
		t.Fatal(err)
	}
	if reqs != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestParseCredentialRequirements_Valid(t *testing.T) {
	raw := `[{"domain":"code-review","capabilities":["go"],"trusted_issuers":["0xAb5801a7D398351b8bE11C439e05C5b3259aec9B"]}]`
	reqs, err := ParseCredentialRequirements(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 requirement, got %d", len(reqs))
	}
	if reqs[0].Domain != "code-review" {
		t.Fatalf("expected domain code-review, got %s", reqs[0].Domain)
	}
}

func TestParseCredentialRequirements_EmptyDomain(t *testing.T) {
	raw := `[{"domain":"","capabilities":["go"]}]`
	_, err := ParseCredentialRequirements(raw)
	if err == nil {
		t.Fatal("expected error for empty domain")
	}
}

func TestParseCredentialRequirements_EmptyCapabilities(t *testing.T) {
	raw := `[{"domain":"code-review","capabilities":[]}]`
	_, err := ParseCredentialRequirements(raw)
	if err == nil {
		t.Fatal("expected error for empty capabilities")
	}
}

func TestParseCredentialRequirements_WhitespaceCapabilities(t *testing.T) {
	raw := `[{"domain":"code-review","capabilities":["  "]}]`
	_, err := ParseCredentialRequirements(raw)
	if err == nil {
		t.Fatal("expected error for whitespace-only capabilities")
	}
}

func TestParseCredentialRequirements_InvalidTrustedIssuer(t *testing.T) {
	raw := `[{"domain":"code-review","capabilities":["go"],"trusted_issuers":["not-an-address"]}]`
	_, err := ParseCredentialRequirements(raw)
	if err == nil {
		t.Fatal("expected error for invalid trusted issuer address")
	}
}

func TestValidateAttestation_WhitespaceOnlyDomain(t *testing.T) {
	att, bidder := makeTestAttestation(t)
	att.Domain = "   "
	err := ValidateAttestation(att, bidder, time.Now())
	if err == nil {
		t.Fatal("expected error for whitespace-only domain")
	}
}

func TestValidateAttestation_WhitespaceOnlyNonce(t *testing.T) {
	att, bidder := makeTestAttestation(t)
	att.Nonce = "  "
	err := ValidateAttestation(att, bidder, time.Now())
	if err == nil {
		t.Fatal("expected error for whitespace-only nonce")
	}
}

func TestValidateAttestation_WhitespaceOnlyCapabilities(t *testing.T) {
	att, bidder := makeTestAttestation(t)
	att.Capabilities = []string{"  ", ""}
	err := ValidateAttestation(att, bidder, time.Now())
	if err == nil {
		t.Fatal("expected error for whitespace-only capabilities")
	}
}

func TestParseAttestations_Empty(t *testing.T) {
	atts, err := ParseAttestations("")
	if err != nil {
		t.Fatal(err)
	}
	if atts != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestParseAttestations_Valid(t *testing.T) {
	raw := `[{"profile":"attestation-v1","issuer_address":"0xAb5801a7D398351b8bE11C439e05C5b3259aec9B","subject_address":"0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045","domain":"code-review","capabilities":["go","solidity"],"issued_at":1700000000,"expires_at":1700100000,"nonce":"nonce-abc","signature":"0x00"}]`
	atts, err := ParseAttestations(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attestation, got %d", len(atts))
	}
	if atts[0].Domain != "code-review" {
		t.Fatalf("expected domain code-review, got %s", atts[0].Domain)
	}
	if atts[0].Profile != attestationV1Profile {
		t.Fatalf("expected profile %s, got %s", attestationV1Profile, atts[0].Profile)
	}
	if len(atts[0].Capabilities) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(atts[0].Capabilities))
	}
}
