package dct

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
)

const CanonicalProfile = "dct-profile-v1"

var (
	ErrInvalidAttenuation = errors.New("delegation must strictly attenuate parent token")
	ErrExpiredToken       = errors.New("token is expired")
	ErrRevokedToken       = errors.New("token is revoked")
	ErrInvalidProfile     = errors.New("token profile is not canonical dct-profile-v1")
	ErrInvalidChain       = errors.New("invalid delegation chain")
	ErrInactiveEscrow     = errors.New("escrow is inactive for dct operations")
)

type Service struct {
	DB  *storage.DB
	Now func() time.Time
}

type MintParams struct {
	EscrowID   int64
	Subject    string
	Issuer     string
	Operations []string
	Resources  []string
	ExpiresAt  int64
}

type DelegateParams struct {
	ParentToken string
	Subject     string
	Issuer      string
	Operations  []string
	Resources   []string
	ExpiresAt   int64
}

type RevokeParams struct {
	TokenID string
	Reason  string
	By      string
}

type Introspection struct {
	Token   *storage.DCTToken `json:"token"`
	Active  bool              `json:"active"`
	Reasons []string          `json:"reasons,omitempty"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func canonicalize(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(strings.ToLower(item))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

// dedupSortReasons deduplicates and sorts reason strings without lowercasing,
// preserving the original casing of each reason.
func dedupSortReasons(reasons []string) []string {
	seen := make(map[string]struct{}, len(reasons))
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}

func toJSON(items []string) (string, error) {
	b, err := json.Marshal(canonicalize(items))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func fromJSON(raw string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return canonicalize(out), nil
}

func canonicalCaveatsJSON(ops, resources []string, expiresAt int64) (string, error) {
	caveats := make([]string, 0, len(ops)+len(resources)+1)
	for _, op := range canonicalize(ops) {
		caveats = append(caveats, "op="+op)
	}
	for _, r := range canonicalize(resources) {
		caveats = append(caveats, "res="+r)
	}
	caveats = append(caveats, "exp<="+strconv.FormatInt(expiresAt, 10))
	b, err := json.Marshal(caveats)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func isSubset(sub, sup []string) bool {
	if len(sub) > len(sup) {
		return false
	}
	set := make(map[string]struct{}, len(sup))
	for _, v := range sup {
		set[v] = struct{}{}
	}
	for _, v := range sub {
		if _, ok := set[v]; !ok {
			return false
		}
	}
	return true
}

func randomID(prefix string, bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}

func randomSecret(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

func validateMintInput(p MintParams) error {
	if p.EscrowID <= 0 {
		return errors.New("escrow_id must be > 0")
	}
	if strings.TrimSpace(p.Subject) == "" {
		return errors.New("subject is required")
	}
	if len(canonicalize(p.Operations)) == 0 {
		return errors.New("operations must be non-empty")
	}
	if len(canonicalize(p.Resources)) == 0 {
		return errors.New("resources must be non-empty")
	}
	if p.ExpiresAt <= 0 {
		return errors.New("expires_at must be a unix timestamp")
	}
	return nil
}

func isEscrowInactive(e *storage.Escrow) bool {
	if e.Frozen {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(e.Status)) {
	case "settled", "refunded", "cancelled", "resolved":
		return true
	default:
		return false
	}
}

func (s *Service) Mint(ctx context.Context, p MintParams) (*storage.DCTToken, string, error) {
	if err := validateMintInput(p); err != nil {
		return nil, "", err
	}
	escrow, err := s.DB.GetEscrow(ctx, p.EscrowID)
	if err != nil {
		return nil, "", fmt.Errorf("escrow lookup: %w", err)
	}
	if isEscrowInactive(escrow) {
		return nil, "", ErrInactiveEscrow
	}
	if p.ExpiresAt <= s.now().Unix() {
		return nil, "", ErrExpiredToken
	}

	ops := canonicalize(p.Operations)
	resources := canonicalize(p.Resources)
	opsJSON, err := toJSON(ops)
	if err != nil {
		return nil, "", err
	}
	resJSON, err := toJSON(resources)
	if err != nil {
		return nil, "", err
	}
	caveatsJSON, err := canonicalCaveatsJSON(ops, resources, p.ExpiresAt)
	if err != nil {
		return nil, "", err
	}
	secret, err := randomSecret(24)
	if err != nil {
		return nil, "", err
	}
	tokenID, err := randomID("dct_", 12)
	if err != nil {
		return nil, "", err
	}
	tokenHash := hashToken(secret)
	rec, err := s.DB.CreateDCTToken(ctx, &storage.DCTToken{
		TokenID:        tokenID,
		TokenHash:      tokenHash,
		EscrowID:       p.EscrowID,
		Subject:        strings.ToLower(p.Subject),
		Issuer:         strings.ToLower(firstNonEmpty(p.Issuer, p.Subject)),
		OperationsJSON: opsJSON,
		ResourcesJSON:  resJSON,
		Profile:        CanonicalProfile,
		CaveatsJSON:    caveatsJSON,
		Depth:          0,
		ExpiresAt:      p.ExpiresAt,
	})
	if err != nil {
		return nil, "", err
	}
	return rec, tokenID + "." + secret, nil
}

func strictAttenuation(childOps, parentOps, childResources, parentResources []string, childExpiry, parentExpiry int64) bool {
	if !isSubset(childOps, parentOps) || !isSubset(childResources, parentResources) || childExpiry > parentExpiry {
		return false
	}
	return len(childOps) < len(parentOps) || len(childResources) < len(parentResources) || childExpiry < parentExpiry
}

func (s *Service) Delegate(ctx context.Context, p DelegateParams) (*storage.DCTToken, string, error) {
	parent, parentActive, reasons, err := s.Introspect(ctx, p.ParentToken)
	if err != nil {
		return nil, "", err
	}
	if !parentActive {
		if slices.Contains(reasons, "expired") || slices.Contains(reasons, "ancestor_expired") {
			return nil, "", ErrExpiredToken
		}
		// escrow_frozen and escrow_terminal_or_inactive indicate the escrow is no
		// longer accepting operations, not that the token itself was revoked.
		if slices.Contains(reasons, "escrow_frozen") || slices.Contains(reasons, "escrow_terminal_or_inactive") {
			return nil, "", ErrInactiveEscrow
		}
		return nil, "", ErrRevokedToken
	}
	if strings.TrimSpace(p.Subject) == "" {
		return nil, "", errors.New("subject is required")
	}
	parentOps, err := fromJSON(parent.OperationsJSON)
	if err != nil {
		return nil, "", fmt.Errorf("parent operations decode: %w", err)
	}
	parentResources, err := fromJSON(parent.ResourcesJSON)
	if err != nil {
		return nil, "", fmt.Errorf("parent resources decode: %w", err)
	}
	ops := canonicalize(p.Operations)
	resources := canonicalize(p.Resources)
	if len(ops) == 0 || len(resources) == 0 {
		return nil, "", errors.New("operations/resources must be non-empty")
	}
	if p.ExpiresAt <= s.now().Unix() {
		return nil, "", ErrInvalidAttenuation
	}
	if !strictAttenuation(ops, parentOps, resources, parentResources, p.ExpiresAt, parent.ExpiresAt) {
		return nil, "", ErrInvalidAttenuation
	}

	opsJSON, err := toJSON(ops)
	if err != nil {
		return nil, "", err
	}
	resJSON, err := toJSON(resources)
	if err != nil {
		return nil, "", err
	}
	caveatsJSON, err := canonicalCaveatsJSON(ops, resources, p.ExpiresAt)
	if err != nil {
		return nil, "", err
	}
	secret, err := randomSecret(24)
	if err != nil {
		return nil, "", err
	}
	tokenID, err := randomID("dct_", 12)
	if err != nil {
		return nil, "", err
	}
	issuer := strings.ToLower(firstNonEmpty(p.Issuer, parent.Subject))
	rec, err := s.DB.CreateDCTToken(ctx, &storage.DCTToken{
		TokenID:        tokenID,
		TokenHash:      hashToken(secret),
		ParentTokenID:  parent.TokenID,
		EscrowID:       parent.EscrowID,
		Subject:        strings.ToLower(p.Subject),
		Issuer:         issuer,
		OperationsJSON: opsJSON,
		ResourcesJSON:  resJSON,
		Profile:        CanonicalProfile,
		CaveatsJSON:    caveatsJSON,
		Depth:          parent.Depth + 1,
		ExpiresAt:      p.ExpiresAt,
	})
	if err != nil {
		return nil, "", err
	}
	return rec, tokenID + "." + secret, nil
}

func parsePresentedToken(token string) (tokenID, secret string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", errors.New("invalid token format")
	}
	return parts[0], parts[1], nil
}

func (s *Service) validateChain(ctx context.Context, leaf *storage.DCTToken) ([]string, error) {
	reasons := make([]string, 0)
	if leaf.Profile != CanonicalProfile {
		reasons = append(reasons, "non_canonical_profile")
	}
	escrow, err := s.DB.GetEscrow(ctx, leaf.EscrowID)
	if err != nil {
		return nil, err
	}
	if escrow.Frozen {
		reasons = append(reasons, "escrow_frozen")
	} else if isEscrowInactive(escrow) {
		reasons = append(reasons, "escrow_terminal_or_inactive")
	}

	seen := map[string]struct{}{leaf.TokenID: {}}
	current := leaf
	for current.ParentTokenID != "" {
		parent, err := s.DB.GetDCTTokenByTokenID(ctx, current.ParentTokenID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				reasons = append(reasons, "missing_ancestor")
				break
			}
			return nil, err
		}
		if _, ok := seen[parent.TokenID]; ok {
			reasons = append(reasons, "lineage_cycle")
			break
		}
		seen[parent.TokenID] = struct{}{}

		if parent.Profile != CanonicalProfile {
			reasons = append(reasons, "ancestor_non_canonical_profile")
		}
		if parent.RevokedAt != nil {
			reasons = append(reasons, "ancestor_revoked")
		}
		if parent.ExpiresAt <= s.now().Unix() {
			reasons = append(reasons, "ancestor_expired")
		}
		if current.EscrowID != parent.EscrowID {
			reasons = append(reasons, "lineage_escrow_mismatch")
		}
		if !strings.EqualFold(current.Issuer, parent.Subject) {
			reasons = append(reasons, "lineage_issuer_subject_mismatch")
		}
		if current.ExpiresAt > parent.ExpiresAt {
			reasons = append(reasons, "lineage_expiry_violation")
		}
		currentOps, err := fromJSON(current.OperationsJSON)
		if err != nil {
			return nil, fmt.Errorf("decode operations: %w", err)
		}
		parentOps, err := fromJSON(parent.OperationsJSON)
		if err != nil {
			return nil, fmt.Errorf("decode parent operations: %w", err)
		}
		currentResources, err := fromJSON(current.ResourcesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode resources: %w", err)
		}
		parentResources, err := fromJSON(parent.ResourcesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode parent resources: %w", err)
		}
		if !isSubset(currentOps, parentOps) || !isSubset(currentResources, parentResources) {
			reasons = append(reasons, "lineage_scope_violation")
		}
		current = parent
	}
	return dedupSortReasons(reasons), nil
}

func (s *Service) Introspect(ctx context.Context, presentedToken string) (*storage.DCTToken, bool, []string, error) {
	tokenID, secret, err := parsePresentedToken(presentedToken)
	if err != nil {
		return nil, false, nil, err
	}
	rec, err := s.DB.GetDCTTokenByTokenID(ctx, tokenID)
	if err != nil {
		return nil, false, nil, err
	}
	reasons := make([]string, 0)
	if subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(hashToken(secret))) != 1 {
		return nil, false, nil, errors.New("token verification failed")
	}
	if rec.RevokedAt != nil {
		reasons = append(reasons, "revoked")
	}
	if rec.ExpiresAt <= s.now().Unix() {
		reasons = append(reasons, "expired")
	}
	chainReasons, err := s.validateChain(ctx, rec)
	if err != nil {
		return nil, false, nil, err
	}
	reasons = append(reasons, chainReasons...)
	reasons = canonicalize(reasons)
	return rec, len(reasons) == 0, reasons, nil
}

func (s *Service) Revoke(ctx context.Context, p RevokeParams) error {
	if strings.TrimSpace(p.TokenID) == "" {
		return errors.New("token_id is required")
	}
	if err := s.DB.RevokeDCTToken(ctx, p.TokenID, firstNonEmpty(p.Reason, "revoked"), firstNonEmpty(p.By, "manual")); err != nil {
		return err
	}
	return nil
}

func (s *Service) RevokeByEscrow(ctx context.Context, escrowID int64, reason, by string) (int64, error) {
	return s.DB.RevokeDCTTokensByEscrow(ctx, escrowID, reason, by)
}

func (s *Service) GetByTokenID(ctx context.Context, tokenID string) (*storage.DCTToken, error) {
	return s.DB.GetDCTTokenByTokenID(ctx, tokenID)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
