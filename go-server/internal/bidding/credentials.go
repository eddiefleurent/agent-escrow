package bidding

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Attestation is a signed capability attestation presented by a bidder (attestation-v1 profile).
// The issuer endorses that subject_address has the listed capabilities in the given domain.
type Attestation struct {
	Profile        string   `json:"profile"`
	IssuerAddress  string   `json:"issuer_address"`
	IssuerDID      string   `json:"issuer_did,omitempty"`
	SubjectAddress string   `json:"subject_address"`
	Domain         string   `json:"domain"`
	Capabilities   []string `json:"capabilities"`
	IssuedAt       int64    `json:"issued_at"`
	ExpiresAt      int64    `json:"expires_at"`
	Nonce          string   `json:"nonce"`
	Signature      string   `json:"signature"`
}

// CredentialRequirement is a buyer-specified credential filter for an RFQ.
type CredentialRequirement struct {
	Domain         string   `json:"domain"`
	Capabilities   []string `json:"capabilities"`
	TrustedIssuers []string `json:"trusted_issuers,omitempty"`
}

// VerificationResult captures the outcome of attestation verification against RFQ requirements.
type VerificationResult struct {
	Verified bool     `json:"verified"`
	Reasons  []string `json:"reasons,omitempty"`
	Matched  int      `json:"matched"`
	Required int      `json:"required"`
}

const attestationV1Profile = "attestation-v1"

// CanonicalAttestationMessage builds the deterministic message that was signed by the issuer.
// The canonical form is: "attestation-v1|issuer|subject|domain|cap1,cap2,...|issued_at|expires_at|nonce"
// All addresses are lowercased, capabilities are sorted and lowercased.
func CanonicalAttestationMessage(a *Attestation) string {
	caps := make([]string, len(a.Capabilities))
	for i, c := range a.Capabilities {
		caps[i] = strings.ToLower(strings.TrimSpace(c))
	}
	sort.Strings(caps)

	return strings.Join([]string{
		attestationV1Profile,
		strings.ToLower(common.HexToAddress(a.IssuerAddress).Hex()),
		strings.ToLower(common.HexToAddress(a.SubjectAddress).Hex()),
		strings.ToLower(strings.TrimSpace(a.Domain)),
		strings.Join(caps, ","),
		strconv.FormatInt(a.IssuedAt, 10),
		strconv.FormatInt(a.ExpiresAt, 10),
		a.Nonce,
	}, "|")
}

// VerifyAttestationSignature checks that the secp256k1 signature over the canonical message
// recovers to the claimed issuer_address.
func VerifyAttestationSignature(a *Attestation) error {
	if a.Profile != attestationV1Profile {
		return fmt.Errorf("unsupported attestation profile: %q (expected %q)", a.Profile, attestationV1Profile)
	}
	if !common.IsHexAddress(a.IssuerAddress) {
		return errors.New("invalid issuer_address")
	}
	if !common.IsHexAddress(a.SubjectAddress) {
		return errors.New("invalid subject_address")
	}

	msg := CanonicalAttestationMessage(a)
	msgHash := crypto.Keccak256Hash([]byte(msg))

	sigBytes := common.FromHex(a.Signature)
	if len(sigBytes) != 65 {
		return fmt.Errorf("invalid signature length: expected 65 bytes, got %d", len(sigBytes))
	}

	// The message is hashed with raw Keccak256 (not EIP-191 personal_sign prefix).
	// Signers that use eth_sign or personal_sign encode v as 27/28; subtract 27
	// to convert to the 0/1 form expected by crypto.Ecrecover (raw ECDSA recovery).
	if sigBytes[64] >= 27 {
		sigBytes[64] -= 27
	}

	pubKey, err := crypto.Ecrecover(msgHash.Bytes(), sigBytes)
	if err != nil {
		return fmt.Errorf("signature recovery failed: %w", err)
	}

	recoveredPub, err := crypto.UnmarshalPubkey(pubKey)
	if err != nil {
		return fmt.Errorf("unmarshal public key: %w", err)
	}
	recoveredAddr := crypto.PubkeyToAddress(*recoveredPub)

	expectedAddr := common.HexToAddress(a.IssuerAddress)
	if recoveredAddr != expectedAddr {
		return fmt.Errorf("signature mismatch: recovered %s, expected issuer %s", recoveredAddr.Hex(), expectedAddr.Hex())
	}

	return nil
}

// ValidateAttestation checks schema, time bounds, and subject binding.
func ValidateAttestation(a *Attestation, bidder string, now time.Time) error {
	if a.Profile != attestationV1Profile {
		return fmt.Errorf("unsupported profile: %q", a.Profile)
	}
	if strings.TrimSpace(a.Domain) == "" {
		return errors.New("attestation domain is required")
	}
	hasCapability := false
	for _, c := range a.Capabilities {
		if strings.TrimSpace(c) != "" {
			hasCapability = true
			break
		}
	}
	if !hasCapability {
		return errors.New("attestation must include at least one capability")
	}
	if strings.TrimSpace(a.Nonce) == "" {
		return errors.New("attestation nonce is required")
	}

	nowUnix := now.Unix()
	if a.IssuedAt > nowUnix {
		return errors.New("attestation issued_at is in the future")
	}
	if a.ExpiresAt <= nowUnix {
		return errors.New("attestation has expired")
	}

	bidderAddr := common.HexToAddress(bidder)
	subjectAddr := common.HexToAddress(a.SubjectAddress)
	if bidderAddr != subjectAddr {
		return fmt.Errorf("attestation subject_address %s does not match bidder %s",
			subjectAddr.Hex(), bidderAddr.Hex())
	}

	return VerifyAttestationSignature(a)
}

// MatchRequirements checks whether a set of validated attestations satisfy the RFQ's credential requirements.
func MatchRequirements(requirements []CredentialRequirement, attestations []Attestation) VerificationResult {
	if len(requirements) == 0 {
		return VerificationResult{Verified: true, Required: 0, Matched: 0}
	}

	result := VerificationResult{Required: len(requirements)}
	for _, req := range requirements {
		if matchSingleRequirement(req, attestations) {
			result.Matched++
		} else {
			result.Reasons = append(result.Reasons,
				fmt.Sprintf("unmet requirement: domain=%s, capabilities=%v", req.Domain, req.Capabilities))
		}
	}
	result.Verified = result.Matched == result.Required
	return result
}

func matchSingleRequirement(req CredentialRequirement, attestations []Attestation) bool {
	reqDomain := strings.ToLower(strings.TrimSpace(req.Domain))
	for _, att := range attestations {
		if strings.ToLower(strings.TrimSpace(att.Domain)) != reqDomain {
			continue
		}
		if !capabilitiesCovered(req.Capabilities, att.Capabilities) {
			continue
		}
		if len(req.TrustedIssuers) > 0 && !issuerTrusted(att.IssuerAddress, req.TrustedIssuers) {
			continue
		}
		return true
	}
	return false
}

func capabilitiesCovered(required, provided []string) bool {
	providedSet := make(map[string]bool, len(provided))
	for _, c := range provided {
		providedSet[strings.ToLower(strings.TrimSpace(c))] = true
	}
	for _, c := range required {
		if !providedSet[strings.ToLower(strings.TrimSpace(c))] {
			return false
		}
	}
	return true
}

func issuerTrusted(issuer string, trusted []string) bool {
	issuerAddr := common.HexToAddress(issuer)
	for _, t := range trusted {
		if common.HexToAddress(t) == issuerAddr {
			return true
		}
	}
	return false
}

// ParseCredentialRequirements parses and validates the JSON array of credential requirements from an RFQ.
// Each requirement must have a non-empty domain, at least one capability, and well-formed trusted_issuers
// (each entry must be a valid hex Ethereum address when present).
func ParseCredentialRequirements(raw string) ([]CredentialRequirement, error) {
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var reqs []CredentialRequirement
	if err := json.Unmarshal([]byte(raw), &reqs); err != nil {
		return nil, fmt.Errorf("parse credential requirements: %w", err)
	}
	for i, req := range reqs {
		if strings.TrimSpace(req.Domain) == "" {
			return nil, fmt.Errorf("credential requirement %d: selector.Domain must not be empty", i)
		}
		hasCapability := false
		for _, c := range req.Capabilities {
			if strings.TrimSpace(c) != "" {
				hasCapability = true
				break
			}
		}
		if !hasCapability {
			return nil, fmt.Errorf("credential requirement %d: selector.Capabilities must include at least one non-empty entry", i)
		}
		for j, issuer := range req.TrustedIssuers {
			if !common.IsHexAddress(issuer) {
				return nil, fmt.Errorf("credential requirement %d: trusted_issuers[%d] %q is not a valid hex Ethereum address", i, j, issuer)
			}
		}
	}
	return reqs, nil
}

// ParseAttestations parses the JSON array of attestations from a bid.
func ParseAttestations(raw string) ([]Attestation, error) {
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var atts []Attestation
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		return nil, fmt.Errorf("parse attestations: %w", err)
	}
	return atts, nil
}

// MarshalVerificationResult serializes the verification result to JSON.
func MarshalVerificationResult(vr VerificationResult) string {
	b, err := json.Marshal(vr)
	if err != nil {
		return "{}"
	}
	return string(b)
}
