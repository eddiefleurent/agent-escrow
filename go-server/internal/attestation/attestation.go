// Package attestation implements completion-attestation-v1: signed recursive attestation
// chains for delegation verification (paper §4.8: transitive liability, chain of custody).
package attestation

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

const CompletionAttestationV1 = "completion-attestation-v1"

// CompletionAttestation is a signed attestation that a delegated sub-task was completed.
// The from_address (delegator/buyer) attests that to_address (worker/delegatee) completed
// the work identified by child_escrow_id, producing outcome_hash.
type CompletionAttestation struct {
	Profile       string `json:"profile"`
	LinkID        string `json:"link_id"`
	ParentLinkID  string `json:"parent_link_id,omitempty"`
	FromAddress   string `json:"from_address"`
	ToAddress     string `json:"to_address"`
	ChildEscrowID *int64 `json:"child_escrow_id,omitempty"`
	TaskSpecHash  string `json:"task_spec_hash,omitempty"`
	OutcomeHash   string `json:"outcome_hash,omitempty"`
	IssuedAt      int64  `json:"issued_at"`
	ExpiresAt     int64  `json:"expires_at"`
	Nonce         string `json:"nonce"`
	Signature     string `json:"signature"`
}

// CanonicalCompletionMessage builds the deterministic message for signature verification.
// Format: "completion-attestation-v1|link_id|parent_link_id|from|to|child_escrow_id|task_spec_hash|outcome_hash|issued_at|expires_at|nonce"
func CanonicalCompletionMessage(a *CompletionAttestation) string {
	childEscrowStr := ""
	if a.ChildEscrowID != nil {
		childEscrowStr = strconv.FormatInt(*a.ChildEscrowID, 10)
	}
	return strings.Join([]string{
		CompletionAttestationV1,
		a.LinkID,
		a.ParentLinkID,
		strings.ToLower(common.HexToAddress(a.FromAddress).Hex()),
		strings.ToLower(common.HexToAddress(a.ToAddress).Hex()),
		childEscrowStr,
		a.TaskSpecHash,
		a.OutcomeHash,
		strconv.FormatInt(a.IssuedAt, 10),
		strconv.FormatInt(a.ExpiresAt, 10),
		a.Nonce,
	}, "|")
}

// VerifyCompletionSignature checks that the secp256k1 signature over the canonical message
// recovers to the claimed from_address.
func VerifyCompletionSignature(a *CompletionAttestation) error {
	if a.Profile != CompletionAttestationV1 {
		return fmt.Errorf("unsupported profile: %q (expected %q)", a.Profile, CompletionAttestationV1)
	}
	if !common.IsHexAddress(a.FromAddress) {
		return errors.New("invalid from_address")
	}
	if !common.IsHexAddress(a.ToAddress) {
		return errors.New("invalid to_address")
	}

	msg := CanonicalCompletionMessage(a)
	msgHash := crypto.Keccak256Hash([]byte(msg))

	sigBytes := common.FromHex(a.Signature)
	if len(sigBytes) != 65 {
		return fmt.Errorf("invalid signature length: expected 65 bytes, got %d", len(sigBytes))
	}
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

	expectedAddr := common.HexToAddress(a.FromAddress)
	if recoveredAddr != expectedAddr {
		return fmt.Errorf("signature mismatch: recovered %s, expected from_address %s", recoveredAddr.Hex(), expectedAddr.Hex())
	}
	return nil
}

// ValidateCompletionAttestation checks schema, time bounds, and structural validity.
func ValidateCompletionAttestation(a *CompletionAttestation, now time.Time) error {
	if a.Profile != CompletionAttestationV1 {
		return fmt.Errorf("unsupported profile: %q", a.Profile)
	}
	if strings.TrimSpace(a.LinkID) == "" {
		return errors.New("link_id is required")
	}
	if strings.TrimSpace(a.Nonce) == "" {
		return errors.New("nonce is required")
	}
	if !common.IsHexAddress(a.FromAddress) {
		return errors.New("invalid from_address")
	}
	if !common.IsHexAddress(a.ToAddress) {
		return errors.New("invalid to_address")
	}

	nowUnix := now.Unix()
	if a.IssuedAt > nowUnix {
		return errors.New("issued_at is in the future")
	}
	if a.ExpiresAt <= nowUnix {
		return errors.New("attestation has expired")
	}

	return VerifyCompletionSignature(a)
}

// ChainValidationResult captures the outcome of validating a full attestation chain.
type ChainValidationResult struct {
	Valid          bool     `json:"valid"`
	RootHash       string   `json:"root_hash"`
	LinkCount      int      `json:"link_count"`
	Reasons        []string `json:"reasons,omitempty"`
	CoveredEscrows []int64  `json:"covered_escrows,omitempty"`
}

// ValidateChain validates a set of completion attestations as a DAG:
// - verifies each signature
// - checks for cycles
// - validates parent-link integrity
// - checks that child_escrow_ids reference known child escrows
// childEscrowIDs is the set of known child escrow IDs for the parent escrow.
func ValidateChain(attestations []CompletionAttestation, childEscrowIDs []int64, now time.Time) ChainValidationResult {
	result := ChainValidationResult{LinkCount: len(attestations)}

	if len(attestations) == 0 {
		result.Valid = true
		return result
	}

	linkByID := make(map[string]*CompletionAttestation, len(attestations))
	seenLinkIDs := make(map[string]bool, len(attestations))

	for i := range attestations {
		a := &attestations[i]
		if seenLinkIDs[a.LinkID] {
			result.Reasons = append(result.Reasons, "duplicate link_id: "+a.LinkID)
			return result
		}
		seenLinkIDs[a.LinkID] = true
		linkByID[a.LinkID] = a
	}

	for i := range attestations {
		if err := ValidateCompletionAttestation(&attestations[i], now); err != nil {
			result.Reasons = append(result.Reasons, fmt.Sprintf("link %s: %v", attestations[i].LinkID, err))
			return result
		}
	}

	// Parent-link integrity: every non-root link must reference an existing parent.
	for _, a := range attestations {
		if a.ParentLinkID == "" {
			continue
		}
		if _, ok := linkByID[a.ParentLinkID]; !ok {
			result.Reasons = append(result.Reasons, fmt.Sprintf("link %s references missing parent_link_id %s", a.LinkID, a.ParentLinkID))
			return result
		}
	}

	// Cycle detection via DFS.
	if cycle := detectCycles(attestations); cycle != "" {
		result.Reasons = append(result.Reasons, cycle)
		return result
	}

	// Check child escrow coverage: every child escrow must be referenced by at least one link.
	childEscrowSet := make(map[int64]bool, len(childEscrowIDs))
	for _, id := range childEscrowIDs {
		childEscrowSet[id] = true
	}
	coveredEscrows := make(map[int64]bool)
	for _, a := range attestations {
		if a.ChildEscrowID != nil {
			if len(childEscrowSet) > 0 && !childEscrowSet[*a.ChildEscrowID] {
				result.Reasons = append(result.Reasons, fmt.Sprintf("attestation link %s references unknown child escrow %d", a.LinkID, *a.ChildEscrowID))
				return result
			}
			coveredEscrows[*a.ChildEscrowID] = true
		}
	}
	for _, cid := range childEscrowIDs {
		if !coveredEscrows[cid] {
			result.Reasons = append(result.Reasons, fmt.Sprintf("child escrow %d not covered by any attestation link", cid))
			return result
		}
	}

	// Compute root hash from sorted canonical messages.
	messages := make([]string, len(attestations))
	for i, a := range attestations {
		messages[i] = CanonicalCompletionMessage(&a)
	}
	sort.Strings(messages)
	combined := strings.Join(messages, "\n")
	result.RootHash = crypto.Keccak256Hash([]byte(combined)).Hex()

	sortedCovered := make([]int64, 0, len(coveredEscrows))
	for id := range coveredEscrows {
		sortedCovered = append(sortedCovered, id)
	}
	sort.Slice(sortedCovered, func(i, j int) bool { return sortedCovered[i] < sortedCovered[j] })
	result.CoveredEscrows = sortedCovered
	result.Valid = true
	return result
}

func detectCycles(attestations []CompletionAttestation) string {
	children := make(map[string][]string)
	for _, a := range attestations {
		if a.ParentLinkID != "" {
			children[a.ParentLinkID] = append(children[a.ParentLinkID], a.LinkID)
		}
	}

	visited := make(map[string]int) // 0=unvisited, 1=in-progress, 2=done
	var dfs func(id string) string
	dfs = func(id string) string {
		if visited[id] == 1 {
			return "cycle detected at link_id " + id
		}
		if visited[id] == 2 {
			return ""
		}
		visited[id] = 1
		for _, child := range children[id] {
			if msg := dfs(child); msg != "" {
				return msg
			}
		}
		visited[id] = 2
		return ""
	}

	for _, a := range attestations {
		if msg := dfs(a.LinkID); msg != "" {
			return msg
		}
	}
	return ""
}

// ParseCompletionAttestations parses a JSON array of completion attestations.
func ParseCompletionAttestations(raw string) ([]CompletionAttestation, error) {
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var atts []CompletionAttestation
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		return nil, fmt.Errorf("parse completion attestations: %w", err)
	}
	return atts, nil
}

// MarshalChainValidationResult serializes the result to JSON.
func MarshalChainValidationResult(r ChainValidationResult) string {
	b, err := json.Marshal(r)
	if err != nil {
		return "{}"
	}
	return string(b)
}
