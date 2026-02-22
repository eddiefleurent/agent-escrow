package ap2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/x402"
	"github.com/ethereum/go-ethereum/common"
)

// Service implements the AP2 mandate-to-escrow bridge.
type Service struct {
	DB    *storage.DB
	Chain chain.ChainClient
	Idx   *indexer.Indexer
	Cfg   *config.Config
	X402  *x402.Client
}

// ValidateMandate checks that a mandate envelope is well-formed and has valid constraints.
func (s *Service) ValidateMandate(_ context.Context, env MandateEnvelope) error {
	if env.Type == "" {
		return fmt.Errorf("mandate type is required")
	}
	if env.Type != MandateTypeIntent && env.Type != MandateTypeCart && env.Type != MandateTypePayment {
		return fmt.Errorf("unsupported mandate type: %s", env.Type)
	}
	if env.SignerAddress == "" {
		return fmt.Errorf("signer_address is required")
	}
	if !common.IsHexAddress(env.SignerAddress) {
		return fmt.Errorf("invalid signer_address: %s", env.SignerAddress)
	}
	if env.Signature == "" {
		return fmt.Errorf("signature is required")
	}
	if env.Authorization.From == "" {
		return fmt.Errorf("authorization.from is required")
	}
	if !common.IsHexAddress(env.Authorization.From) {
		return fmt.Errorf("invalid authorization.from: %s", env.Authorization.From)
	}

	signerNorm := strings.ToLower(common.HexToAddress(env.SignerAddress).Hex())
	fromNorm := strings.ToLower(common.HexToAddress(env.Authorization.From).Hex())
	if signerNorm != fromNorm {
		return fmt.Errorf("signer_address and authorization.from must match")
	}

	if env.Authorization.Value == "" {
		return fmt.Errorf("authorization.value is required")
	}
	if _, ok := new(big.Int).SetString(env.Authorization.Value, 10); !ok {
		return fmt.Errorf("invalid authorization.value: %s", env.Authorization.Value)
	}

	return nil
}

// BindToEscrow links a validated mandate to an escrow, verifying amount constraints.
func (s *Service) BindToEscrow(ctx context.Context, env MandateEnvelope, escrowID int64) (*EscrowBinding, error) {
	escrow, err := s.DB.GetEscrow(escrowID)
	if err != nil {
		return nil, fmt.Errorf("escrow not found: %w", err)
	}

	signerNorm := strings.ToLower(common.HexToAddress(env.SignerAddress).Hex())
	buyerNorm := strings.ToLower(common.HexToAddress(escrow.Buyer).Hex())
	if signerNorm != buyerNorm {
		return nil, fmt.Errorf("mandate signer must be the escrow buyer")
	}

	authValue, ok := new(big.Int).SetString(env.Authorization.Value, 10)
	if !ok {
		return nil, fmt.Errorf("invalid authorization value")
	}
	escrowAmount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		return nil, fmt.Errorf("invalid escrow amount")
	}
	if authValue.Cmp(escrowAmount) < 0 {
		return nil, fmt.Errorf("authorization value (%s) is less than escrow amount (%s)", authValue, escrowAmount)
	}

	mandateHash := hashMandate(env)
	mandateID := mandateHash[:16]

	payloadJSON, err := json.Marshal(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	var budgetAmount, budgetCurrency string
	if env.Type == MandateTypeIntent {
		if v, ok := env.Payload["budget_amount"].(string); ok {
			budgetAmount = v
		}
		if v, ok := env.Payload["budget_currency"].(string); ok {
			budgetCurrency = v
		}
	}

	var expiresAt *string
	if env.Type == MandateTypeIntent {
		if ttl, ok := env.Payload["ttl_seconds"].(float64); ok && ttl > 0 {
			t := time.Now().Add(time.Duration(ttl) * time.Second).UTC().Format(time.RFC3339)
			expiresAt = &t
		}
	}

	err = s.DB.CreateAP2Mandate(ctx, mandateID, string(env.Type), mandateHash, signerNorm,
		budgetAmount, budgetCurrency, expiresAt, &escrowID, string(payloadJSON))
	if err != nil {
		return nil, fmt.Errorf("store mandate: %w", err)
	}

	return &EscrowBinding{
		MandateID:   mandateID,
		MandateHash: mandateHash,
		EscrowID:    escrowID,
		Status:      "bound",
	}, nil
}

// FundViaMandate orchestrates the full flow: validate -> bind -> fund on-chain.
func (s *Service) FundViaMandate(ctx context.Context, escrowID int64, env MandateEnvelope) (*FundViaMandateResponse, error) {
	if err := s.ValidateMandate(ctx, env); err != nil {
		return nil, fmt.Errorf("validate mandate: %w", err)
	}

	binding, err := s.BindToEscrow(ctx, env, escrowID)
	if err != nil {
		return nil, fmt.Errorf("bind to escrow: %w", err)
	}

	escrow, err := s.DB.GetEscrow(escrowID)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}

	escrowAddr := common.HexToAddress(escrow.EscrowAddress)
	from := common.HexToAddress(env.Authorization.From)

	validAfter, ok := new(big.Int).SetString(env.Authorization.ValidAfter, 10)
	if !ok {
		validAfter = big.NewInt(0)
	}
	validBefore, ok := new(big.Int).SetString(env.Authorization.ValidBefore, 10)
	if !ok {
		return nil, fmt.Errorf("invalid authorization.valid_before")
	}

	nonceBytes, err := hexToBytes32(env.Authorization.Nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization.nonce: %w", err)
	}

	rBytes, err := hexToBytes32(env.Authorization.R)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization.r: %w", err)
	}
	sBytes, err := hexToBytes32(env.Authorization.S)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization.s: %w", err)
	}

	tx, err := s.Chain.FundWithAuthorization(ctx, escrowAddr, from, validAfter, validBefore, nonceBytes, env.Authorization.V, rBytes, sBytes)
	if err != nil {
		return nil, fmt.Errorf("fund with authorization: %w", err)
	}

	txHash := tx.Hash().Hex()

	if err := s.DB.UpdateAP2MandateFunding(ctx, binding.MandateID, txHash); err != nil {
		slog.Warn("failed to update mandate funding status", "mandate_id", binding.MandateID, "error", err)
	}

	if err := s.Idx.RunOnce(ctx); err != nil {
		slog.Warn("post-fund indexer run failed", "escrow_id", escrowID, "error", err)
	}

	return &FundViaMandateResponse{
		TxHash:    txHash,
		EscrowID:  escrowID,
		MandateID: binding.MandateID,
		Status:    "funded",
	}, nil
}

func hashMandate(env MandateEnvelope) string {
	data, _ := json.Marshal(env)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hexToBytes32(s string) ([32]byte, error) {
	var result [32]byte
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		return result, err
	}
	if len(b) > 32 {
		return result, fmt.Errorf("value exceeds 32 bytes")
	}
	copy(result[32-len(b):], b)
	return result, nil
}
