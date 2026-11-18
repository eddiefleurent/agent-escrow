package ap2

import (
	"bytes"
	"context"
	"database/sql"
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
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	mandateStatusPending = "pending"
	mandateStatusFunded  = "funded"
	mandateStatusFailed  = "failed"
)

// ErrInvalidMandate indicates invalid user-provided AP2 mandate input.
var ErrInvalidMandate = errors.New("invalid mandate")

// ErrBindValidation indicates user-provided bind inputs failed escrow validation checks.
var ErrBindValidation = errors.New("bind validation failed")

// ErrFundingPending means a previous broadcast has no receipt yet. The row
// stays pending with its tx hash; a retry checks that receipt before sending again.
var ErrFundingPending = errors.New("mandate funding is pending confirmation")

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
// EIP-712 signature from signer_address, that its EIP-3009 signature matches the
// signed tuple, and that it is inside its validity window.
func (s *Service) ValidateMandate(ctx context.Context, env MandateEnvelope) error {
	_, err := s.verifyMandate(ctx, env)
	return err
}

// FundViaMandate verifies the mandate and its EIP-3009 authorization, then
// relays fundWithAuthorization. Verification finishes before any row is written
// and before the funding transaction is sent. DOMAIN_SEPARATOR() is a read.
func (s *Service) FundViaMandate(ctx context.Context, escrowID int64, env MandateEnvelope) (*FundViaMandateResponse, error) {
	vm, err := s.verifyMandate(ctx, env)
	if err != nil {
		if errors.Is(err, errChainRead) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalidMandate, err)
	}

	escrow, err := s.DB.GetEscrow(ctx, escrowID)
	if err != nil {
		return nil, fmt.Errorf("get escrow: %w", err)
	}
	if err := validateEscrowBinding(s.Cfg, vm, escrow); err != nil {
		if errors.Is(err, ErrBindValidation) {
			return nil, fmt.Errorf("%w: %w", ErrInvalidMandate, err)
		}
		return nil, err
	}

	claim, err := s.claimMandate(ctx, vm, escrow)
	if err != nil {
		return nil, err
	}
	if claim.funded != nil {
		return &FundViaMandateResponse{
			TxHash:    claim.funded.FundingTxHash,
			EscrowID:  escrowID,
			MandateID: claim.funded.ID,
			Status:    mandateStatusFunded,
		}, nil
	}

	return s.broadcastFunding(ctx, vm, claim.id, escrowID)
}

type mandateClaim struct {
	id     string
	funded *storage.AP2Mandate
}

func (s *Service) claimMandate(ctx context.Context, vm *verifiedMandate, escrow *storage.Escrow) (mandateClaim, error) {
	rec, err := mandateInsert(vm, escrow)
	if err != nil {
		return mandateClaim{}, err
	}
	existing, err := s.DB.GetAP2MandateByAuthorization(ctx, rec.ChainID, rec.Token, rec.Authorizer, rec.AuthorizationNonce)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return mandateClaim{}, err
		}
		if err := s.DB.CreateAP2Mandate(ctx, rec); err != nil {
			if !isUniqueConstraint(err) {
				return mandateClaim{}, fmt.Errorf("store mandate: %w", err)
			}
			existing, err = s.DB.GetAP2MandateByAuthorization(ctx, rec.ChainID, rec.Token, rec.Authorizer, rec.AuthorizationNonce)
			if err != nil {
				return mandateClaim{}, err
			}
		} else {
			return mandateClaim{id: rec.ID}, nil
		}
	}
	return s.reconcileMandate(ctx, existing, rec)
}

func (s *Service) reconcileMandate(ctx context.Context, existing *storage.AP2Mandate, rec storage.AP2MandateInsert) (mandateClaim, error) {
	switch existing.Status {
	case mandateStatusFunded:
		return mandateClaim{funded: existing}, nil
	case mandateStatusPending:
		if existing.FundingTxHash == "" {
			ok, err := s.DB.RefreshPendingAP2Mandate(ctx, existing.ID, rec)
			if err != nil {
				return mandateClaim{}, err
			}
			if !ok {
				return mandateClaim{}, ErrFundingPending
			}
			return mandateClaim{id: existing.ID}, nil
		}
		return s.reconcilePendingTx(ctx, existing, rec)
	case mandateStatusFailed:
		ok, err := s.DB.ReopenFailedAP2Mandate(ctx, existing.ID, rec)
		if err != nil {
			return mandateClaim{}, err
		}
		if !ok {
			again, err := s.DB.GetAP2Mandate(ctx, existing.ID)
			if err != nil {
				return mandateClaim{}, err
			}
			return s.reconcileMandate(ctx, again, rec)
		}
		return mandateClaim{id: existing.ID}, nil
	default:
		return mandateClaim{}, fmt.Errorf("mandate %s has status %q and cannot be funded", existing.ID, existing.Status)
	}
}

func (s *Service) reconcilePendingTx(ctx context.Context, existing *storage.AP2Mandate, rec storage.AP2MandateInsert) (mandateClaim, error) {
	hash := common.HexToHash(existing.FundingTxHash)
	receipt, err := s.Chain.TransactionReceipt(ctx, hash)
	if err != nil {
		if errors.Is(err, ethereum.NotFound) {
			return mandateClaim{}, fmt.Errorf("%w: tx %s", ErrFundingPending, existing.FundingTxHash)
		}
		return mandateClaim{}, fmt.Errorf("check funding receipt %s: %w", existing.FundingTxHash, err)
	}
	if receipt.Status == types.ReceiptStatusSuccessful {
		if err := s.DB.MarkAP2MandateFunded(ctx, existing.ID, existing.FundingTxHash); err != nil {
			return mandateClaim{}, err
		}
		existing.Status = mandateStatusFunded
		return mandateClaim{funded: existing}, nil
	}
	if err := s.DB.MarkAP2MandateFailed(ctx, existing.ID); err != nil {
		return mandateClaim{}, err
	}
	ok, err := s.DB.ReopenFailedAP2Mandate(ctx, existing.ID, rec)
	if err != nil {
		return mandateClaim{}, err
	}
	if !ok {
		return mandateClaim{}, ErrFundingPending
	}
	return mandateClaim{id: existing.ID}, nil
}

func (s *Service) broadcastFunding(ctx context.Context, vm *verifiedMandate, mandateID string, escrowID int64) (*FundViaMandateResponse, error) {
	a := vm.Auth
	tx, err := s.Chain.FundWithAuthorization(ctx, a.To, a.From, a.ValidAfter, a.ValidBefore, a.Nonce, a.V, a.R, a.S)
	if err != nil {
		if markErr := s.DB.MarkAP2MandateFailed(ctx, mandateID); markErr != nil {
			slog.Warn("failed to mark mandate failed after chain error", "mandate_id", mandateID, "error", markErr)
		}
		return nil, fmt.Errorf("fund with authorization: %w", err)
	}
	txHash := tx.Hash().Hex()
	if err := s.DB.SetAP2MandatePendingTx(ctx, mandateID, txHash); err != nil {
		return nil, fmt.Errorf("record funding tx: %w", err)
	}

	receipt, err := chain.WaitMined(ctx, s.Chain, tx.Hash())
	if err != nil {
		slog.Warn("funding receipt not available; mandate stays pending", "mandate_id", mandateID, "tx_hash", txHash, "error", err)
		return nil, fmt.Errorf("%w: tx %s: %w", ErrFundingPending, txHash, err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		if markErr := s.DB.MarkAP2MandateFailed(ctx, mandateID); markErr != nil {
			slog.Warn("failed to mark reverted mandate failed", "mandate_id", mandateID, "error", markErr)
		}
		return nil, fmt.Errorf("fund with authorization reverted (status %d)", receipt.Status)
	}
	if err := s.DB.MarkAP2MandateFunded(ctx, mandateID, txHash); err != nil {
		return nil, fmt.Errorf("record funded mandate: %w", err)
	}
	if s.Idx != nil {
		if err := s.Idx.RunOnce(ctx); err != nil {
			slog.Warn("post-fund indexer run failed", "escrow_id", escrowID, "error", err)
		}
	}
	return &FundViaMandateResponse{
		TxHash:    txHash,
		EscrowID:  escrowID,
		MandateID: mandateID,
		Status:    mandateStatusFunded,
	}, nil
}

// validateEscrowBinding checks the signed authorization against the escrow.
// fundWithAuthorization pulls the escrow's amount of the escrow's token, so
// both must match the signed tuple exactly.
func validateEscrowBinding(cfg *config.Config, vm *verifiedMandate, escrow *storage.Escrow) error {
	if cfg == nil || escrow.ChainID != cfg.ChainID {
		return fmt.Errorf("%w: escrow chain_id does not match the server chain", ErrBindValidation)
	}
	if !common.IsHexAddress(escrow.Buyer) || vm.Signer != common.HexToAddress(escrow.Buyer) {
		return fmt.Errorf("%w: mandate signer must be the escrow buyer", ErrBindValidation)
	}
	if !common.IsHexAddress(escrow.EscrowAddress) || vm.Auth.To != common.HexToAddress(escrow.EscrowAddress) {
		return fmt.Errorf("%w: authorization.to (%s) does not match escrow address (%s)", ErrBindValidation, vm.Auth.To.Hex(), escrow.EscrowAddress)
	}
	if !common.IsHexAddress(escrow.Token) || common.HexToAddress(escrow.Token) == (common.Address{}) {
		return fmt.Errorf("%w: escrow is not funded with an ERC-20 token", ErrBindValidation)
	}
	if vm.Auth.Token != common.HexToAddress(escrow.Token) {
		return fmt.Errorf("%w: authorization.token (%s) does not match escrow token (%s)", ErrBindValidation, vm.Auth.Token.Hex(), escrow.Token)
	}
	escrowAmount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		return errors.New("invalid escrow amount")
	}
	if vm.Auth.Value.Cmp(escrowAmount) != 0 {
		return fmt.Errorf("%w: authorization value (%s) must equal escrow amount (%s)", ErrBindValidation, vm.Auth.Value, escrowAmount)
	}
	return nil
}

func mandateInsert(vm *verifiedMandate, escrow *storage.Escrow) (storage.AP2MandateInsert, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, vm.RawPayload); err != nil {
		return storage.AP2MandateInsert{}, fmt.Errorf("compact payload: %w", err)
	}
	payload := compact.String()
	expiresAt := vm.ExpiresAt.UTC().Format(time.RFC3339)
	mandateHash := hex.EncodeToString(vm.Digest.Bytes())
	return storage.AP2MandateInsert{
		ID:                 mandateHash[:16],
		MandateType:        string(vm.Type),
		MandateHash:        mandateHash,
		SignerAddress:      strings.ToLower(vm.Signer.Hex()),
		BudgetAmount:       vm.BudgetAmount,
		BudgetCurrency:     vm.BudgetCurrency,
		ExpiresAt:          &expiresAt,
		EscrowID:           &escrow.ID,
		RawPayload:         payload,
		ChainID:            escrow.ChainID,
		Token:              strings.ToLower(common.HexToAddress(escrow.Token).Hex()),
		Authorizer:         strings.ToLower(vm.Signer.Hex()),
		AuthorizationNonce: hexutil.Encode(vm.Auth.Nonce[:]),
	}, nil
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
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
