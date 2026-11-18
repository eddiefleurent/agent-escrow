package ap2

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// ErrInvalidMandate indicates invalid user-provided AP2 mandate input.
var ErrInvalidMandate = errors.New("invalid mandate")

// ErrBindValidation indicates user-provided bind inputs failed escrow validation checks.
var ErrBindValidation = errors.New("bind validation failed")

// Service implements the AP2 mandate-to-escrow bridge.
type Service struct {
	DB    *storage.DB
	Chain chain.ChainClient
	Idx   *indexer.Indexer
	Cfg   *config.Config
	X402  *x402.Client
	// Now overrides the clock used for mandate validity checks; nil means time.Now.
	Now func() time.Time
}

// ValidateMandate checks that a mandate envelope is well-formed, carries a valid
// EIP-712 signature from signer_address, and is inside its validity window.
func (s *Service) ValidateMandate(_ context.Context, env MandateEnvelope) error {
	_, err := s.verifyMandate(env)
	return err
}

// bindToEscrow links a verified mandate to an escrow, checking it against the
// escrow's buyer, address, and amount. It takes a *verifiedMandate so that an
// unverified envelope can never be bound.
func (s *Service) bindToEscrow(ctx context.Context, vm *verifiedMandate, escrowID int64) (*EscrowBinding, error) {
	escrow, err := s.DB.GetEscrow(ctx, escrowID)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}

	if !common.IsHexAddress(escrow.Buyer) || vm.Signer != common.HexToAddress(escrow.Buyer) {
		return nil, fmt.Errorf("%w: mandate signer must be the escrow buyer", ErrBindValidation)
	}

	// EIP-3009 authorization recipient must match the escrow contract address
	if !common.IsHexAddress(escrow.EscrowAddress) || vm.Auth.To != common.HexToAddress(escrow.EscrowAddress) {
		return nil, fmt.Errorf("%w: authorization.to (%s) does not match escrow address (%s)", ErrBindValidation, vm.Auth.To.Hex(), escrow.EscrowAddress)
	}

	escrowAmount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		return nil, errors.New("invalid escrow amount")
	}
	if vm.Auth.Value.Cmp(escrowAmount) < 0 {
		return nil, fmt.Errorf("%w: authorization value (%s) is less than escrow amount (%s)", ErrBindValidation, vm.Auth.Value, escrowAmount)
	}

	// The mandate hash is the EIP-712 digest the signer signed, so anyone can
	// recompute it from the mandate contents.
	mandateHash := hex.EncodeToString(vm.Digest.Bytes())
	mandateID := mandateHash[:16]

	var compact bytes.Buffer
	if err := json.Compact(&compact, vm.RawPayload); err != nil {
		return nil, fmt.Errorf("compact payload: %w", err)
	}

	expiresAt := vm.ExpiresAt.UTC().Format(time.RFC3339)
	signerNorm := strings.ToLower(vm.Signer.Hex())
	err = s.DB.CreateAP2Mandate(ctx, mandateID, string(vm.Type), mandateHash, signerNorm,
		vm.BudgetAmount, vm.BudgetCurrency, &expiresAt, &escrowID, compact.String())
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
	vm, err := s.verifyMandate(env)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidMandate, err)
	}

	binding, err := s.bindToEscrow(ctx, vm, escrowID)
	if err != nil {
		if errors.Is(err, ErrBindValidation) {
			return nil, fmt.Errorf("%w: %w", ErrInvalidMandate, err)
		}
		return nil, err
	}

	escrowAddr := vm.Auth.To
	a := vm.Auth
	tx, err := s.Chain.FundWithAuthorization(ctx, escrowAddr, a.From, a.ValidAfter, a.ValidBefore, a.Nonce, a.V, a.R, a.S)
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

func hexToBytes32(s string) ([32]byte, error) {
	var result [32]byte
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		return result, err
	}
	if len(b) > 32 {
		return result, errors.New("value exceeds 32 bytes")
	}
	copy(result[32-len(b):], b)
	return result, nil
}
