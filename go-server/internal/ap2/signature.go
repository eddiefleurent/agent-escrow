package ap2

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// EIP-712 domain for AP2 mandate signatures. The domain binds every mandate
// signature to one deployment: chainId is the server's configured chain and
// verifyingContract is the configured TaskEscrowFactory. A mandate signed for
// another chain or another factory recovers to a different address and is rejected.
const (
	EIP712DomainName    = "agent-escrow AP2"
	EIP712DomainVersion = "1"
)

// maxIssuedAtSkew bounds how far in the future a mandate's issued_at may be,
// to tolerate modest clock drift between the signer and this server.
const maxIssuedAtSkew = 5 * time.Minute

// mandateTypes is the EIP-712 type set. Each mandate type is its own primary
// type, so a signature over one type can never be replayed as another. Every
// mandate embeds the EIP-3009 Authorization it is paired with, so a mandate
// signature cannot be reused with a different transfer authorization.
var mandateTypes = apitypes.Types{
	"EIP712Domain": {
		{Name: "name", Type: "string"},
		{Name: "version", Type: "string"},
		{Name: "chainId", Type: "uint256"},
		{Name: "verifyingContract", Type: "address"},
	},
	"Authorization": {
		{Name: "from", Type: "address"},
		{Name: "to", Type: "address"},
		{Name: "value", Type: "uint256"},
		{Name: "validAfter", Type: "uint256"},
		{Name: "validBefore", Type: "uint256"},
		{Name: "nonce", Type: "bytes32"},
	},
	"IntentMandate": {
		{Name: "signer", Type: "address"},
		{Name: "budgetAmount", Type: "uint256"},
		{Name: "budgetCurrency", Type: "string"},
		{Name: "ttlSeconds", Type: "uint256"},
		{Name: "issuedAt", Type: "uint256"},
		{Name: "description", Type: "string"},
		{Name: "authorization", Type: "Authorization"},
	},
	"CartMandate": {
		{Name: "signer", Type: "address"},
		{Name: "merchant", Type: "address"},
		{Name: "amount", Type: "uint256"},
		{Name: "currency", Type: "string"},
		{Name: "itemsHash", Type: "bytes32"},
		{Name: "ttlSeconds", Type: "uint256"},
		{Name: "issuedAt", Type: "uint256"},
		{Name: "authorization", Type: "Authorization"},
	},
	"PaymentMandate": {
		{Name: "signer", Type: "address"},
		{Name: "amount", Type: "uint256"},
		{Name: "currency", Type: "string"},
		{Name: "recipient", Type: "address"},
		{Name: "nonce", Type: "bytes32"},
		{Name: "authorization", Type: "Authorization"},
	},
}

var primaryTypes = map[MandateType]string{
	MandateTypeIntent:  "IntentMandate",
	MandateTypeCart:    "CartMandate",
	MandateTypePayment: "PaymentMandate",
}

// parsedAuthorization holds the decoded EIP-3009 authorization fields.
type parsedAuthorization struct {
	From        common.Address
	To          common.Address
	Value       *big.Int
	ValidAfter  *big.Int
	ValidBefore *big.Int
	Nonce       [32]byte
	R           [32]byte
	S           [32]byte
	V           uint8
}

// verifiedMandate is a decoded mandate. Production code obtains it only from
// Service.verifyMandate, which has checked the EIP-712 signature, constraints,
// and validity window, so code that accepts one cannot see an unverified mandate.
type verifiedMandate struct {
	Type           MandateType
	Signer         common.Address
	Auth           parsedAuthorization
	Digest         common.Hash
	ExpiresAt      time.Time
	IssuedAt       time.Time // zero for payment mandates, which have no issued_at
	BudgetAmount   string
	BudgetCurrency string
	RawPayload     []byte
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// verifyMandate performs all mandate checks: structure, EIP-712 signature,
// payload-to-authorization consistency, and the validity window.
func (s *Service) verifyMandate(env MandateEnvelope) (*verifiedMandate, error) {
	if env.Signature == "" {
		return nil, errors.New("signature is required")
	}
	sig, err := parseSignature(env.Signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	vm, err := s.decodeMandate(env)
	if err != nil {
		return nil, err
	}

	recovered, err := recoverSigner(vm.Digest, sig)
	if err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}
	if recovered != vm.Signer {
		return nil, errors.New("signature does not match signer_address")
	}

	// Validity window, checked after the signature so that only fields the
	// signer committed to are trusted. EIP-3009 requires validAfter < now < validBefore.
	now := s.now()
	if vm.Auth.ValidAfter.Cmp(big.NewInt(now.Unix())) >= 0 {
		return nil, errors.New("mandate is not yet valid (authorization.valid_after is in the future)")
	}
	if !vm.IssuedAt.IsZero() && vm.IssuedAt.After(now.Add(maxIssuedAtSkew)) {
		return nil, errors.New("mandate issued_at is in the future")
	}
	if !now.Before(vm.ExpiresAt) {
		return nil, fmt.Errorf("mandate expired at %s", vm.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return vm, nil
}

// decodeMandate parses and cross-checks the envelope and computes the EIP-712
// digest the signer must have signed. It does not check the signature or the
// clock; callers outside tests must use verifyMandate.
func (s *Service) decodeMandate(env MandateEnvelope) (*verifiedMandate, error) {
	if env.Type == "" {
		return nil, errors.New("mandate type is required")
	}
	primary, ok := primaryTypes[env.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported mandate type: %s", env.Type)
	}
	if env.SignerAddress == "" {
		return nil, errors.New("signer_address is required")
	}
	if !common.IsHexAddress(env.SignerAddress) {
		return nil, fmt.Errorf("invalid signer_address: %s", env.SignerAddress)
	}
	signer := common.HexToAddress(env.SignerAddress)

	auth, err := parseAuthorization(env.Authorization)
	if err != nil {
		return nil, err
	}
	if auth.From != signer {
		return nil, errors.New("signer_address and authorization.from must match")
	}

	if s.Cfg == nil {
		return nil, errors.New("ap2 service is not configured")
	}
	if !common.IsHexAddress(s.Cfg.FactoryAddress) {
		return nil, errors.New("ap2 service has no valid factory address for the EIP-712 domain")
	}

	if len(bytes.TrimSpace(env.Payload)) == 0 {
		return nil, errors.New("payload is required")
	}

	vm := &verifiedMandate{Type: env.Type, Signer: signer, Auth: auth, RawPayload: env.Payload}
	// Effective expiry is the earliest of authorization.valid_before and, for
	// intent/cart mandates, issued_at + ttl_seconds.
	expiry := time.Unix(1<<62, 0)
	if auth.ValidBefore.IsInt64() && auth.ValidBefore.Int64() < 1<<62 {
		expiry = time.Unix(auth.ValidBefore.Int64(), 0)
	}

	var message apitypes.TypedDataMessage
	var payloadSigner common.Address
	switch env.Type {
	case MandateTypeIntent:
		var p IntentMandate
		if err := decodeStrict(env.Payload, &p); err != nil {
			return nil, fmt.Errorf("invalid intent payload: %w", err)
		}
		if payloadSigner, err = parseAddress("payload.signer", p.Signer); err != nil {
			return nil, err
		}
		budget, err := parseUint256("payload.budget_amount", p.BudgetAmount)
		if err != nil {
			return nil, err
		}
		if auth.Value.Cmp(budget) > 0 {
			return nil, fmt.Errorf("authorization.value (%s) exceeds intent budget_amount (%s)", auth.Value, budget)
		}
		ttlExpiry, err := ttlWindow(p.IssuedAt, p.TTLSeconds)
		if err != nil {
			return nil, err
		}
		if ttlExpiry.Before(expiry) {
			expiry = ttlExpiry
		}
		vm.IssuedAt = time.Unix(p.IssuedAt, 0)
		vm.BudgetAmount = budget.String()
		vm.BudgetCurrency = p.BudgetCurrency
		message = apitypes.TypedDataMessage{
			"signer":         payloadSigner.Hex(),
			"budgetAmount":   budget,
			"budgetCurrency": p.BudgetCurrency,
			"ttlSeconds":     big.NewInt(p.TTLSeconds),
			"issuedAt":       big.NewInt(p.IssuedAt),
			"description":    p.Description,
		}
	case MandateTypeCart:
		var p CartMandate
		if err := decodeStrict(env.Payload, &p); err != nil {
			return nil, fmt.Errorf("invalid cart payload: %w", err)
		}
		if payloadSigner, err = parseAddress("payload.signer", p.Signer); err != nil {
			return nil, err
		}
		merchant, err := parseAddress("payload.merchant", p.Merchant)
		if err != nil {
			return nil, err
		}
		amount, err := parseUint256("payload.amount", p.Amount)
		if err != nil {
			return nil, err
		}
		if auth.Value.Cmp(amount) != 0 {
			return nil, fmt.Errorf("authorization.value (%s) must equal cart amount (%s)", auth.Value, amount)
		}
		itemsHash, err := parseBytes32Strict("payload.items_hash", p.ItemsHash)
		if err != nil {
			return nil, err
		}
		ttlExpiry, err := ttlWindow(p.IssuedAt, p.TTLSeconds)
		if err != nil {
			return nil, err
		}
		if ttlExpiry.Before(expiry) {
			expiry = ttlExpiry
		}
		vm.IssuedAt = time.Unix(p.IssuedAt, 0)
		message = apitypes.TypedDataMessage{
			"signer":     payloadSigner.Hex(),
			"merchant":   merchant.Hex(),
			"amount":     amount,
			"currency":   p.Currency,
			"itemsHash":  itemsHash,
			"ttlSeconds": big.NewInt(p.TTLSeconds),
			"issuedAt":   big.NewInt(p.IssuedAt),
		}
	case MandateTypePayment:
		var p PaymentMandate
		if err := decodeStrict(env.Payload, &p); err != nil {
			return nil, fmt.Errorf("invalid payment payload: %w", err)
		}
		if payloadSigner, err = parseAddress("payload.signer", p.Signer); err != nil {
			return nil, err
		}
		recipient, err := parseAddress("payload.recipient", p.Recipient)
		if err != nil {
			return nil, err
		}
		if recipient != auth.To {
			return nil, errors.New("payload.recipient must equal authorization.to")
		}
		amount, err := parseUint256("payload.amount", p.Amount)
		if err != nil {
			return nil, err
		}
		if auth.Value.Cmp(amount) != 0 {
			return nil, fmt.Errorf("authorization.value (%s) must equal payment amount (%s)", auth.Value, amount)
		}
		nonce, err := parseBytes32Strict("payload.nonce", p.Nonce)
		if err != nil {
			return nil, err
		}
		message = apitypes.TypedDataMessage{
			"signer":    payloadSigner.Hex(),
			"amount":    amount,
			"currency":  p.Currency,
			"recipient": recipient.Hex(),
			"nonce":     nonce,
		}
	}
	if payloadSigner != signer {
		return nil, errors.New("payload.signer must equal signer_address")
	}
	message["authorization"] = apitypes.TypedDataMessage{
		"from":        auth.From.Hex(),
		"to":          auth.To.Hex(),
		"value":       auth.Value,
		"validAfter":  auth.ValidAfter,
		"validBefore": auth.ValidBefore,
		"nonce":       auth.Nonce,
	}

	digest, err := mandateDigest(s.Cfg.ChainID, common.HexToAddress(s.Cfg.FactoryAddress), primary, message)
	if err != nil {
		return nil, fmt.Errorf("compute mandate digest: %w", err)
	}
	vm.Digest = digest
	vm.ExpiresAt = expiry
	return vm, nil
}

// mandateDigest returns the EIP-712 digest keccak256("\x19\x01" || domainSeparator || hashStruct(message)).
func mandateDigest(chainID int64, verifyingContract common.Address, primaryType string, message apitypes.TypedDataMessage) (common.Hash, error) {
	td := apitypes.TypedData{
		Types:       mandateTypes,
		PrimaryType: primaryType,
		Domain: apitypes.TypedDataDomain{
			Name:              EIP712DomainName,
			Version:           EIP712DomainVersion,
			ChainId:           (*math.HexOrDecimal256)(big.NewInt(chainID)),
			VerifyingContract: verifyingContract.Hex(),
		},
		Message: message,
	}
	hash, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return common.Hash{}, err
	}
	return common.BytesToHash(hash), nil
}

// parseSignature decodes a 65-byte [r || s || v] secp256k1 signature, accepting
// v in {0,1,27,28}, and rejects high-s values (EIP-2) to prevent malleability.
func parseSignature(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return nil, errors.New("must be 0x-prefixed hex")
	}
	sig, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, errors.New("not valid hex")
	}
	if len(sig) != crypto.SignatureLength {
		return nil, fmt.Errorf("expected %d bytes, got %d", crypto.SignatureLength, len(sig))
	}
	v := sig[64]
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return nil, fmt.Errorf("invalid recovery id %d", sig[64])
	}
	r := new(big.Int).SetBytes(sig[:32])
	sv := new(big.Int).SetBytes(sig[32:64])
	if !crypto.ValidateSignatureValues(v, r, sv, true) {
		return nil, errors.New("invalid r/s values (s must be in the lower half of the curve order)")
	}
	sig[64] = v
	return sig, nil
}

func recoverSigner(digest common.Hash, sig []byte) (common.Address, error) {
	pub, err := crypto.SigToPub(digest.Bytes(), sig)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

func parseAuthorization(a EIP3009Authorization) (parsedAuthorization, error) {
	var out parsedAuthorization
	var err error
	if a.From == "" {
		return out, errors.New("authorization.from is required")
	}
	if out.From, err = parseAddress("authorization.from", a.From); err != nil {
		return out, err
	}
	if a.To == "" {
		return out, errors.New("authorization.to is required")
	}
	if out.To, err = parseAddress("authorization.to", a.To); err != nil {
		return out, err
	}
	if a.Value == "" {
		return out, errors.New("authorization.value is required")
	}
	if out.Value, err = parseUint256("authorization.value", a.Value); err != nil {
		return out, err
	}
	out.ValidAfter = new(big.Int)
	if a.ValidAfter != "" {
		if out.ValidAfter, err = parseUint256("authorization.valid_after", a.ValidAfter); err != nil {
			return out, err
		}
	}
	if a.ValidBefore == "" {
		return out, errors.New("authorization.valid_before is required")
	}
	if out.ValidBefore, err = parseUint256("authorization.valid_before", a.ValidBefore); err != nil {
		return out, err
	}
	if out.ValidBefore.Cmp(out.ValidAfter) <= 0 {
		return out, fmt.Errorf("authorization.valid_before (%s) must be > valid_after (%s)", out.ValidBefore, out.ValidAfter)
	}
	if a.Nonce == "" {
		return out, errors.New("authorization.nonce is required")
	}
	if out.Nonce, err = hexToBytes32(a.Nonce); err != nil {
		return out, fmt.Errorf("invalid authorization.nonce: %w", err)
	}
	if a.R == "" {
		return out, errors.New("authorization.r is required")
	}
	if out.R, err = hexToBytes32(a.R); err != nil {
		return out, fmt.Errorf("invalid authorization.r: %w", err)
	}
	if a.S == "" {
		return out, errors.New("authorization.s is required")
	}
	if out.S, err = hexToBytes32(a.S); err != nil {
		return out, fmt.Errorf("invalid authorization.s: %w", err)
	}
	out.V = a.V
	return out, nil
}

// ttlWindow returns issuedAt+ttlSeconds after checking both are positive.
func ttlWindow(issuedAt, ttlSeconds int64) (time.Time, error) {
	if issuedAt <= 0 {
		return time.Time{}, errors.New("payload.issued_at must be a positive unix timestamp")
	}
	if ttlSeconds <= 0 {
		return time.Time{}, errors.New("payload.ttl_seconds must be positive")
	}
	if issuedAt > (1<<62)-ttlSeconds {
		return time.Time{}, errors.New("payload.issued_at + ttl_seconds overflows")
	}
	return time.Unix(issuedAt+ttlSeconds, 0), nil
}

// decodeStrict decodes a single JSON object, rejecting unknown fields so that
// every accepted payload field is covered by the signature.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func parseAddress(field, s string) (common.Address, error) {
	if !common.IsHexAddress(s) {
		return common.Address{}, fmt.Errorf("invalid %s: %q", field, s)
	}
	return common.HexToAddress(s), nil
}

// parseUint256 parses a canonical base-10 uint256 (no sign, no leading zeros).
func parseUint256(field, s string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("%s is required", field)
	}
	if s != "0" && s[0] == '0' {
		return nil, fmt.Errorf("invalid %s: %q (leading zeros not allowed)", field, s)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("invalid %s: %q (must be a base-10 integer)", field, s)
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.BitLen() > 256 {
		return nil, fmt.Errorf("invalid %s: %q", field, s)
	}
	return v, nil
}

// parseBytes32Strict requires exactly 32 bytes of 0x-prefixed hex.
func parseBytes32Strict(field, s string) ([32]byte, error) {
	var out [32]byte
	if !strings.HasPrefix(s, "0x") || len(s) != 66 {
		return out, fmt.Errorf("invalid %s: must be 0x-prefixed 32-byte hex", field)
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return out, fmt.Errorf("invalid %s: %w", field, err)
	}
	copy(out[:], b)
	return out, nil
}
