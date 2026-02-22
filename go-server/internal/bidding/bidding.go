package bidding

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Service encapsulates the bidding protocol business logic shared by MCP and HTTP handlers.
type Service struct {
	DB    *storage.DB
	Chain chain.ChainClient
	Idx   *indexer.Indexer
	Cfg   *config.Config
}

type CreateRFQParams struct {
	Title                    string
	Description              string
	Buyer                    string
	Token                    string
	BudgetMin                string
	BudgetMax                string
	Deadline                 int64
	ReviewPeriodSeconds      int64
	DisputePeriodSeconds     int64
	ArbitratorTimeoutSeconds int64
	Verifier                 string
	Arbitrator               string
	WorkerStake              string
	MilestonesJSON           string
	RequirementsJSON         string
	ExpiresAt                int64
}

type PlaceBidParams struct {
	RFQID             int64
	Bidder            string
	Amount            string
	EstimatedDuration int64
	ReputationBond    string
	MilestonesJSON    string
	Message           string
	ExpiresAt         int64
}

type AcceptBidParams struct {
	RFQID  int64
	BidID  int64
	Caller string // must match RFQ buyer
}

type AcceptBidResult struct {
	Bid    *storage.Bid
	Escrow *storage.Escrow
	Task   *storage.Task
	TxHash string
}

func (s *Service) CreateRFQ(p CreateRFQParams) (*storage.RFQ, error) {
	budgetMin, ok := new(big.Int).SetString(p.BudgetMin, 10)
	if !ok || budgetMin.Sign() < 0 {
		return nil, fmt.Errorf("invalid budget_min")
	}
	budgetMax, ok := new(big.Int).SetString(p.BudgetMax, 10)
	if !ok || budgetMax.Sign() <= 0 {
		return nil, fmt.Errorf("invalid budget_max")
	}
	if budgetMin.Cmp(budgetMax) > 0 {
		return nil, fmt.Errorf("budget_min must be <= budget_max")
	}

	now := time.Now().Unix()
	if p.ExpiresAt <= now {
		return nil, fmt.Errorf("expires_at must be in the future")
	}
	if p.Deadline <= now {
		return nil, fmt.Errorf("deadline must be in the future")
	}

	if !common.IsHexAddress(p.Buyer) {
		return nil, fmt.Errorf("invalid buyer address")
	}

	if p.Token != "" && p.Token != "0x0000000000000000000000000000000000000000" {
		if !common.IsHexAddress(p.Token) {
			return nil, fmt.Errorf("invalid token address")
		}
	}

	if p.Verifier != "" && !common.IsHexAddress(p.Verifier) {
		return nil, fmt.Errorf("invalid verifier address")
	}
	if p.Arbitrator != "" && !common.IsHexAddress(p.Arbitrator) {
		return nil, fmt.Errorf("invalid arbitrator address")
	}

	if p.WorkerStake == "" {
		p.WorkerStake = "0"
	}
	if _, ok := new(big.Int).SetString(p.WorkerStake, 10); !ok {
		return nil, fmt.Errorf("invalid worker_stake")
	}

	if p.MilestonesJSON == "" {
		p.MilestonesJSON = "[]"
	}
	if p.RequirementsJSON == "" {
		p.RequirementsJSON = "{}"
	}

	specHash := crypto.Keccak256Hash([]byte(p.Title + p.Description))

	rfq, err := s.DB.CreateRFQ(&storage.RFQ{
		Title:                    p.Title,
		Description:              p.Description,
		SpecHash:                 specHash.Hex(),
		Buyer:                    p.Buyer,
		Token:                    p.Token,
		BudgetMin:                p.BudgetMin,
		BudgetMax:                p.BudgetMax,
		Deadline:                 p.Deadline,
		ReviewPeriodSeconds:      p.ReviewPeriodSeconds,
		DisputePeriodSeconds:     p.DisputePeriodSeconds,
		ArbitratorTimeoutSeconds: p.ArbitratorTimeoutSeconds,
		Verifier:                 p.Verifier,
		Arbitrator:               p.Arbitrator,
		WorkerStake:              p.WorkerStake,
		MilestonesJSON:           p.MilestonesJSON,
		RequirementsJSON:         p.RequirementsJSON,
		Status:                   "open",
		ExpiresAt:                p.ExpiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create rfq: %w", err)
	}
	return rfq, nil
}

func (s *Service) PlaceBid(p PlaceBidParams) (*storage.Bid, error) {
	rfq, err := s.DB.GetRFQ(p.RFQID)
	if err != nil {
		return nil, fmt.Errorf("rfq not found: %w", err)
	}
	if rfq.Status != "open" {
		return nil, fmt.Errorf("rfq is not open (status: %s)", rfq.Status)
	}

	now := time.Now().Unix()
	if rfq.ExpiresAt <= now {
		return nil, fmt.Errorf("rfq has expired")
	}

	if !common.IsHexAddress(p.Bidder) {
		return nil, fmt.Errorf("invalid bidder address")
	}
	if p.Bidder == rfq.Buyer {
		return nil, fmt.Errorf("bidder cannot be the same as the rfq buyer")
	}

	amount, ok := new(big.Int).SetString(p.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amount")
	}
	budgetMin, _ := new(big.Int).SetString(rfq.BudgetMin, 10)
	budgetMax, _ := new(big.Int).SetString(rfq.BudgetMax, 10)
	if amount.Cmp(budgetMin) < 0 || amount.Cmp(budgetMax) > 0 {
		return nil, fmt.Errorf("amount must be between budget_min (%s) and budget_max (%s)", rfq.BudgetMin, rfq.BudgetMax)
	}

	if p.ExpiresAt <= now {
		return nil, fmt.Errorf("bid expires_at must be in the future")
	}
	if p.ExpiresAt > rfq.ExpiresAt {
		return nil, fmt.Errorf("bid expires_at must not exceed rfq expires_at")
	}

	if p.ReputationBond == "" {
		p.ReputationBond = "0"
	}
	if _, ok := new(big.Int).SetString(p.ReputationBond, 10); !ok {
		return nil, fmt.Errorf("invalid reputation_bond")
	}

	if p.MilestonesJSON == "" {
		p.MilestonesJSON = "[]"
	}

	bid, err := s.DB.CreateBid(&storage.Bid{
		RFQID:             p.RFQID,
		Bidder:            p.Bidder,
		Amount:            p.Amount,
		EstimatedDuration: p.EstimatedDuration,
		ReputationBond:    p.ReputationBond,
		MilestonesJSON:    p.MilestonesJSON,
		Message:           p.Message,
		Status:            "pending",
		ExpiresAt:         p.ExpiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create bid: %w", err)
	}
	return bid, nil
}

// AcceptBid accepts a bid, creates an on-chain escrow, and updates the database.
func (s *Service) AcceptBid(ctx context.Context, p AcceptBidParams) (*AcceptBidResult, error) {
	rfq, err := s.DB.GetRFQ(p.RFQID)
	if err != nil {
		return nil, fmt.Errorf("rfq not found: %w", err)
	}
	if rfq.Status != "open" {
		return nil, fmt.Errorf("rfq is not open (status: %s)", rfq.Status)
	}
	if p.Caller != "" && p.Caller != rfq.Buyer {
		return nil, fmt.Errorf("only the rfq buyer can accept bids")
	}

	bid, err := s.DB.GetBid(p.BidID)
	if err != nil {
		return nil, fmt.Errorf("bid not found: %w", err)
	}
	if bid.RFQID != p.RFQID {
		return nil, fmt.Errorf("bid does not belong to this rfq")
	}
	if bid.Status != "pending" {
		return nil, fmt.Errorf("bid is not pending (status: %s)", bid.Status)
	}

	now := time.Now().Unix()
	if bid.ExpiresAt <= now {
		return nil, fmt.Errorf("bid has expired")
	}

	amount, ok := new(big.Int).SetString(bid.Amount, 10)
	if !ok {
		return nil, fmt.Errorf("invalid bid amount")
	}

	workerStakeVal := big.NewInt(0)
	if rfq.WorkerStake != "" && rfq.WorkerStake != "0" {
		ws, ok := new(big.Int).SetString(rfq.WorkerStake, 10)
		if !ok {
			return nil, fmt.Errorf("invalid rfq worker_stake")
		}
		workerStakeVal = ws
	}

	specHash := crypto.Keccak256Hash([]byte(rfq.Title + rfq.Description))

	var tokenAddr common.Address
	if rfq.Token != "" && rfq.Token != "0x0000000000000000000000000000000000000000" {
		tokenAddr = common.HexToAddress(rfq.Token)
	}

	factory := common.HexToAddress(s.Cfg.FactoryAddress)
	params := chain.CreateEscrowParams{
		Buyer:                    common.HexToAddress(rfq.Buyer),
		Worker:                   common.HexToAddress(bid.Bidder),
		Verifier:                 common.HexToAddress(rfq.Verifier),
		Arbitrator:               common.HexToAddress(rfq.Arbitrator),
		Amount:                   amount,
		WorkerStake:              workerStakeVal,
		SubmissionDeadline:       uint64(rfq.Deadline),
		ReviewPeriodSeconds:      uint64(rfq.ReviewPeriodSeconds),
		DisputePeriodSeconds:     uint64(rfq.DisputePeriodSeconds),
		TaskSpecHash:             specHash,
		ArbitratorTimeoutSeconds: uint64(rfq.ArbitratorTimeoutSeconds),
		Token:                    tokenAddr,
	}

	tx, err := s.Chain.CreateEscrow(ctx, factory, params)
	if err != nil {
		return nil, fmt.Errorf("chain CreateEscrow: %w", err)
	}

	result, err := chain.WaitMinedAndParseEscrow(ctx, s.Chain, tx.Hash())
	if err != nil {
		return nil, fmt.Errorf("receipt error: %w", err)
	}

	task, err := s.DB.CreateTask(rfq.Title, rfq.Description, specHash.Hex())
	if err != nil {
		return nil, fmt.Errorf("db CreateTask: %w", err)
	}

	escrow, err := s.DB.CreateEscrow(&storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  s.Cfg.ChainID,
		FactoryAddress:           s.Cfg.FactoryAddress,
		EscrowAddress:            result.EscrowAddress.Hex(),
		EscrowID:                 result.EscrowID,
		Buyer:                    rfq.Buyer,
		Worker:                   bid.Bidder,
		Verifier:                 rfq.Verifier,
		Arbitrator:               rfq.Arbitrator,
		Amount:                   bid.Amount,
		WorkerStake:              workerStakeVal.String(),
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       rfq.Deadline,
		ReviewPeriodSeconds:      rfq.ReviewPeriodSeconds,
		DisputePeriodSeconds:     rfq.DisputePeriodSeconds,
		ArbitratorTimeoutSeconds: rfq.ArbitratorTimeoutSeconds,
		MilestoneCount:           1,
		CurrentMilestone:         0,
		ActiveWorker:             bid.Bidder,
	})
	if err != nil {
		return nil, fmt.Errorf("db CreateEscrow: %w", err)
	}

	if err := s.DB.AcceptBid(bid.ID, escrow.ID); err != nil {
		return nil, fmt.Errorf("db AcceptBid: %w", err)
	}
	if err := s.DB.UpdateRFQStatus(rfq.ID, "closed"); err != nil {
		return nil, fmt.Errorf("db UpdateRFQStatus: %w", err)
	}
	if err := s.DB.RejectPendingBids(rfq.ID, bid.ID); err != nil {
		return nil, fmt.Errorf("db RejectPendingBids: %w", err)
	}

	_ = s.Idx.RunOnce(ctx)

	updatedBid, err := s.DB.GetBid(bid.ID)
	if err != nil {
		return nil, fmt.Errorf("db GetBid after accept: %w", err)
	}

	return &AcceptBidResult{
		Bid:    updatedBid,
		Escrow: escrow,
		Task:   task,
		TxHash: tx.Hash().Hex(),
	}, nil
}
