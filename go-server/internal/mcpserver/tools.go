package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/a2a"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/ap2"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/bidding"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type milestoneArg struct {
	Amount             string `json:"amount" jsonschema:"Milestone amount in wei or smallest unit"`
	SubmissionDeadline string `json:"submission_deadline" jsonschema:"Unix timestamp for milestone submission deadline"`
}

type createEscrowArgs struct {
	Title                    string         `json:"title" jsonschema:"Task title"`
	Description              string         `json:"description" jsonschema:"Task description"`
	Buyer                    string         `json:"buyer" jsonschema:"Buyer address"`
	Worker                   string         `json:"worker" jsonschema:"Worker address"`
	Verifier                 string         `json:"verifier" jsonschema:"Verifier address"`
	Arbitrator               string         `json:"arbitrator" jsonschema:"Arbitrator address"`
	Amount                   string         `json:"amount" jsonschema:"Total amount in wei (ETH) or smallest unit (ERC20)"`
	WorkerStake              string         `json:"worker_stake,omitempty" jsonschema:"Worker anti-Sybil stake in wei or smallest unit; omit or 0 for no stake"`
	SubmissionDeadline       string         `json:"submission_deadline" jsonschema:"Unix timestamp for submission deadline"`
	ReviewPeriodSeconds      string         `json:"review_period_seconds" jsonschema:"Review period in seconds"`
	DisputePeriodSeconds     string         `json:"dispute_period_seconds" jsonschema:"Dispute period in seconds"`
	ArbitratorTimeoutSeconds string         `json:"arbitrator_timeout_seconds" jsonschema:"Arbitrator timeout in seconds"`
	Token                    string         `json:"token,omitempty" jsonschema:"ERC20 token address; omit or use 0x0000000000000000000000000000000000000000 for ETH"`
	Milestones               []milestoneArg `json:"milestones,omitempty" jsonschema:"Optional array of milestones; omit for single-milestone (V1) escrow"`
	BackupWorker             string         `json:"backup_worker,omitempty" jsonschema:"Optional backup worker address; omit for no backup agent"`
	BackupDeadlineExtension  string         `json:"backup_deadline_extension,omitempty" jsonschema:"Seconds to extend deadline when backup activates; omit or 0 for no extension"`
}

type escrowIDArgs struct {
	EscrowID string `json:"escrow_id" jsonschema:"Database escrow ID"`
}

type submitArgs struct {
	EscrowID       string `json:"escrow_id" jsonschema:"Database escrow ID"`
	SubmissionURI  string `json:"submission_uri" jsonschema:"URI of submission"`
	MilestoneIndex string `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type approveArgs struct {
	EscrowID       string `json:"escrow_id" jsonschema:"Database escrow ID"`
	Role           string `json:"role" jsonschema:"Role: buyer or verifier"`
	MilestoneIndex string `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type disputeArgs struct {
	EscrowID       string `json:"escrow_id" jsonschema:"Database escrow ID"`
	Role           string `json:"role" jsonschema:"Role: buyer, verifier, or worker"`
	ReasonURI      string `json:"reason_uri" jsonschema:"URI describing reason"`
	MilestoneIndex string `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type resolveArgs struct {
	EscrowID       string `json:"escrow_id" jsonschema:"Database escrow ID"`
	WorkerAwardBps string `json:"worker_award_bps" jsonschema:"Worker award basis points 0-10000"`
	ResolutionURI  string `json:"resolution_uri" jsonschema:"URI of resolution"`
	MilestoneIndex string `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type listArgs struct {
	Role    string `json:"role" jsonschema:"Filter by role"`
	Address string `json:"address" jsonschema:"Address for role filter"`
	Status  string `json:"status" jsonschema:"Filter by status"`
}

type createRFQArgs struct {
	Title                    string `json:"title" jsonschema:"Task title for the RFQ"`
	Description              string `json:"description" jsonschema:"Task description for the RFQ"`
	Buyer                    string `json:"buyer" jsonschema:"Buyer address broadcasting the RFQ"`
	Token                    string `json:"token,omitempty" jsonschema:"ERC20 token address; omit or 0x0 for ETH"`
	BudgetMin                string `json:"budget_min" jsonschema:"Minimum budget in wei or smallest unit"`
	BudgetMax                string `json:"budget_max" jsonschema:"Maximum budget in wei or smallest unit"`
	Deadline                 string `json:"deadline" jsonschema:"Unix timestamp: latest acceptable submission deadline"`
	ReviewPeriodSeconds      string `json:"review_period_seconds" jsonschema:"Review period in seconds"`
	DisputePeriodSeconds     string `json:"dispute_period_seconds" jsonschema:"Dispute period in seconds"`
	ArbitratorTimeoutSeconds string `json:"arbitrator_timeout_seconds" jsonschema:"Arbitrator timeout in seconds"`
	Verifier                 string `json:"verifier,omitempty" jsonschema:"Designated verifier address"`
	Arbitrator               string `json:"arbitrator,omitempty" jsonschema:"Designated arbitrator address"`
	WorkerStake              string `json:"worker_stake,omitempty" jsonschema:"Required worker stake; omit or 0 for none"`
	MilestonesJSON           string `json:"milestones_json,omitempty" jsonschema:"JSON array of milestone specs"`
	RequirementsJSON         string `json:"requirements_json,omitempty" jsonschema:"JSON: capability requirements, tags, constraints"`
	ExpiresAt                string `json:"expires_at" jsonschema:"Unix timestamp: RFQ expiry"`
}

type placeBidArgs struct {
	RFQID             string `json:"rfq_id" jsonschema:"RFQ ID to bid on"`
	Bidder            string `json:"bidder" jsonschema:"Worker agent address placing the bid"`
	Amount            string `json:"amount" jsonschema:"Proposed total price in wei or smallest unit"`
	EstimatedDuration string `json:"estimated_duration,omitempty" jsonschema:"Estimated seconds to complete"`
	ReputationBond    string `json:"reputation_bond,omitempty" jsonschema:"Offered reputation bond in wei"`
	MilestonesJSON    string `json:"milestones_json,omitempty" jsonschema:"JSON: proposed milestone breakdown"`
	Message           string `json:"message,omitempty" jsonschema:"Free-form bid justification"`
	ExpiresAt         string `json:"expires_at" jsonschema:"Unix timestamp: bid expiry"`
	StakeMandateID    string `json:"stake_mandate_id,omitempty" jsonschema:"Optional AP2 mandate ID for Sybil-resistant stake-on-bid (paper §6)"`
}

type listBidsArgs struct {
	RFQID  string `json:"rfq_id,omitempty" jsonschema:"List bids for this RFQ ID"`
	Bidder string `json:"bidder,omitempty" jsonschema:"List bids by this bidder address"`
}

type acceptBidArgs struct {
	RFQID  string `json:"rfq_id" jsonschema:"RFQ ID"`
	BidID  string `json:"bid_id" jsonschema:"Bid ID to accept"`
	Caller string `json:"caller,omitempty" jsonschema:"Caller address (must match RFQ buyer)"`
}

type reputationArgs struct {
	Address string `json:"address" jsonschema:"Ethereum address to look up reputation for"`
	Role    string `json:"role,omitempty" jsonschema:"Optional: 'worker' or 'buyer'. Omit to return both roles."`
}

type fundViaMandateArgs struct {
	EscrowID        string `json:"escrow_id" jsonschema:"Database escrow ID to fund"`
	MandateType     string `json:"mandate_type" jsonschema:"AP2 mandate type: intent, cart, or payment"`
	SignerAddress   string `json:"signer_address" jsonschema:"Mandate signer address (must be escrow buyer)"`
	Signature       string `json:"signature" jsonschema:"Cryptographic signature of the mandate"`
	Payload         string `json:"payload,omitempty" jsonschema:"JSON string of mandate payload (budget, items, etc.)"`
	AuthFrom        string `json:"auth_from" jsonschema:"EIP-3009 authorization from address"`
	AuthTo          string `json:"auth_to" jsonschema:"EIP-3009 authorization to address (must match escrow address)"`
	AuthValue       string `json:"auth_value" jsonschema:"EIP-3009 authorization value"`
	AuthValidAfter  string `json:"auth_valid_after" jsonschema:"EIP-3009 validAfter timestamp"`
	AuthValidBefore string `json:"auth_valid_before" jsonschema:"EIP-3009 validBefore timestamp"`
	AuthNonce       string `json:"auth_nonce" jsonschema:"EIP-3009 nonce (hex)"`
	AuthV           string `json:"auth_v" jsonschema:"EIP-3009 signature v"`
	AuthR           string `json:"auth_r" jsonschema:"EIP-3009 signature r (hex)"`
	AuthS           string `json:"auth_s" jsonschema:"EIP-3009 signature s (hex)"`
}

type subscribeEventsArgs struct {
	EscrowID    string `json:"escrow_id,omitempty" jsonschema:"Filter to specific escrow address; omit for all"`
	SinceID     string `json:"since_id,omitempty" jsonschema:"Return events after this event ID (cursor-based pagination)"`
	Granularity string `json:"granularity,omitempty" jsonschema:"Granularity level: L0, L1, L2, L3 (default L1)"`
	Limit       string `json:"limit,omitempty" jsonschema:"Max events to return (default 50, max 200)"`
}

type emptyArgs struct{}

func (s *Server) registerTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_escrow",
		Description: "Create a new task and escrow contract via the factory",
	}, s.handleCreateEscrow)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fund_escrow",
		Description: "Fund an escrow as the buyer",
	}, s.handleFundEscrow)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "deposit_stake",
		Description: "Deposit worker anti-Sybil stake into escrow (required before submission when workerStake > 0)",
	}, s.handleDepositStake)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "submit_work",
		Description: "Submit work as the worker",
	}, s.handleSubmitWork)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "approve_work",
		Description: "Approve work as buyer or verifier",
	}, s.handleApproveWork)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "dispute_work",
		Description: "Dispute or reject submitted work",
	}, s.handleDisputeWork)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "resolve_dispute",
		Description: "Resolve a dispute as the arbitrator",
	}, s.handleResolveDispute)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_escrow",
		Description: "Get escrow details",
	}, s.handleGetEscrow)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_escrows",
		Description: "List escrows, optionally filtered by role and status",
	}, s.handleListEscrows)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "abort_remaining_milestones",
		Description: "Abort remaining milestones after a terminal failure (buyer only, multi-milestone escrows)",
	}, s.handleAbortRemainingMilestones)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "activate_backup",
		Description: "Activate the backup worker, replacing the current worker (buyer only, requires backup_worker to be set at creation)",
	}, s.handleActivateBackup)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_reputation",
		Description: "Get on-chain reputation record for an address (tasks completed, disputed, failed). Paper §4.6: immutable ledger approach.",
	}, s.handleGetReputation)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_rfq",
		Description: "Broadcast a Task_RFQ (Request for Quote) describing a task for agents to bid on. Paper §6.1: Task_RFQ broadcast.",
	}, s.handleCreateRFQ)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "place_bid",
		Description: "Submit a Bid_Object on an open RFQ with proposed price, duration, and bond. Paper §6.1: signed Bid_Objects.",
	}, s.handlePlaceBid)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_bids",
		Description: "List bids for an RFQ (buyer view) or by bidder address (worker view).",
	}, s.handleListBids)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "accept_bid",
		Description: "Accept a bid on an RFQ; triggers on-chain escrow creation with bid parameters. Paper §6.1: bid acceptance formalizes into escrow.",
	}, s.handleAcceptBid)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fund_via_mandate",
		Description: "Fund an escrow via an AP2 mandate with EIP-3009 gasless authorization. Paper §6: AP2 stake-on-bid + conditional settlement.",
	}, s.handleFundViaMandate)

	if s.bus != nil && s.cfg.EventsEnabled {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "subscribe_events",
			Description: "Poll recent escrow lifecycle events with cursor-based pagination. Returns events since a given cursor (event ID). Paper §4.5: configurable granularity L0-L3.",
		}, s.handleSubscribeEvents)
	}

	if s.cfg.A2AEnabled {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "get_agent_card",
			Description: "Get the A2A agent card JSON for this settlement agent. Returns capabilities, skills, and the A2A endpoint URL for agent discovery. Paper §6: A2A settlement adapter.",
		}, s.handleGetAgentCard)
	}
}

func (s *Server) handleCreateEscrow(ctx context.Context, req *mcp.CallToolRequest, args createEscrowArgs) (*mcp.CallToolResult, any, error) {
	amount, ok := new(big.Int).SetString(args.Amount, 10)
	if !ok {
		return textResult("invalid amount"), nil, nil
	}
	if err := chain.ValidateComplexityFloor(amount, s.cfg.ComplexityFloor); err != nil {
		return textResult(err.Error()), nil, nil
	}
	deadline, err := strconv.ParseUint(args.SubmissionDeadline, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid submission_deadline: %v", err)), nil, nil
	}
	review, err := strconv.ParseUint(args.ReviewPeriodSeconds, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid review_period_seconds: %v", err)), nil, nil
	}
	dispute, err := strconv.ParseUint(args.DisputePeriodSeconds, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid dispute_period_seconds: %v", err)), nil, nil
	}
	arbTimeout, err := strconv.ParseUint(args.ArbitratorTimeoutSeconds, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid arbitrator_timeout_seconds: %v", err)), nil, nil
	}

	workerStakeVal := big.NewInt(0)
	if args.WorkerStake != "" {
		ws, ok := new(big.Int).SetString(args.WorkerStake, 10)
		if !ok {
			return textResult("invalid worker_stake"), nil, nil
		}
		workerStakeVal = ws
	}

	specHash := crypto.Keccak256Hash([]byte(args.Title + args.Description))

	var tokenAddr common.Address
	if args.Token != "" {
		if !common.IsHexAddress(args.Token) {
			return textResult("invalid token address"), nil, nil
		}
		tokenAddr = common.HexToAddress(args.Token)
	}

	var milestones []chain.MilestoneParam
	for _, m := range args.Milestones {
		msAmount, ok := new(big.Int).SetString(m.Amount, 10)
		if !ok {
			return textResult(fmt.Sprintf("invalid milestone amount: %s", m.Amount)), nil, nil
		}
		msDeadline, err := strconv.ParseUint(m.SubmissionDeadline, 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid milestone submission_deadline: %v", err)), nil, nil
		}
		milestones = append(milestones, chain.MilestoneParam{
			Amount:             msAmount,
			SubmissionDeadline: msDeadline,
		})
	}

	var backupWorkerAddr common.Address
	if args.BackupWorker != "" {
		if !common.IsHexAddress(args.BackupWorker) {
			return textResult("invalid backup_worker address"), nil, nil
		}
		backupWorkerAddr = common.HexToAddress(args.BackupWorker)
	}
	var backupDeadlineExt uint64
	if args.BackupDeadlineExtension != "" {
		bde, err := strconv.ParseUint(args.BackupDeadlineExtension, 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid backup_deadline_extension: %v", err)), nil, nil
		}
		backupDeadlineExt = bde
	}
	if backupDeadlineExt > 0 && (args.BackupWorker == "" || common.HexToAddress(args.BackupWorker) == (common.Address{})) {
		return textResult("backup_deadline_extension set but no backup_worker provided"), nil, nil
	}

	factory := common.HexToAddress(s.cfg.FactoryAddress)
	params := chain.CreateEscrowParams{
		Buyer:                    common.HexToAddress(args.Buyer),
		Worker:                   common.HexToAddress(args.Worker),
		Verifier:                 common.HexToAddress(args.Verifier),
		Arbitrator:               common.HexToAddress(args.Arbitrator),
		Amount:                   amount,
		WorkerStake:              workerStakeVal,
		SubmissionDeadline:       deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		TaskSpecHash:             specHash,
		ArbitratorTimeoutSeconds: arbTimeout,
		Token:                    tokenAddr,
		Milestones:               milestones,
		BackupWorker:             backupWorkerAddr,
		BackupDeadlineExtension:  backupDeadlineExt,
	}

	tx, err := s.chain.CreateEscrow(ctx, factory, params)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}

	result, err := chain.WaitMinedAndParseEscrow(ctx, s.chain, tx.Hash())
	if err != nil {
		return textResult(fmt.Sprintf("receipt error: %v", err)), nil, nil
	}

	task, err := s.db.CreateTask(args.Title, args.Description, specHash.Hex())
	if err != nil {
		return textResult(fmt.Sprintf("db error: %v", err)), nil, nil
	}

	milestoneCount := 1
	if len(milestones) > 0 {
		milestoneCount = len(milestones)
	}

	escrow, err := s.db.CreateEscrow(&storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  s.cfg.ChainID,
		FactoryAddress:           s.cfg.FactoryAddress,
		EscrowAddress:            result.EscrowAddress.Hex(),
		EscrowID:                 result.EscrowID,
		Buyer:                    args.Buyer,
		Worker:                   args.Worker,
		Verifier:                 args.Verifier,
		Arbitrator:               args.Arbitrator,
		Amount:                   args.Amount,
		WorkerStake:              workerStakeVal.String(),
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       int64(deadline),
		ReviewPeriodSeconds:      int64(review),
		DisputePeriodSeconds:     int64(dispute),
		ArbitratorTimeoutSeconds: int64(arbTimeout),
		MilestoneCount:           milestoneCount,
		CurrentMilestone:         0,
		BackupWorker:             backupWorkerAddr.Hex(),
		BackupDeadlineExtension:  int64(backupDeadlineExt),
		ActiveWorker:             args.Worker,
	})
	if err != nil {
		return textResult(fmt.Sprintf("db error: %v", err)), nil, nil
	}

	for i, m := range milestones {
		_, err := s.db.CreateMilestone(&storage.MilestoneRecord{
			EscrowID:           escrow.ID,
			MilestoneIndex:     i,
			Amount:             m.Amount.String(),
			SubmissionDeadline: int64(m.SubmissionDeadline),
			Status:             "pending",
		})
		if err != nil {
			return textResult(fmt.Sprintf("db error creating milestone %d: %v", i, err)), nil, nil
		}
	}

	_ = s.idx.RunOnce(ctx)

	return jsonResult(map[string]any{
		"escrow_id":       escrow.ID,
		"tx_hash":         tx.Hash().Hex(),
		"task_id":         task.ID,
		"escrow_address":  result.EscrowAddress.Hex(),
		"chain_escrow_id": result.EscrowID,
		"milestone_count": milestoneCount,
	})
}

func (s *Server) handleFundEscrow(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	amount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		return textResult(fmt.Sprintf("malformed escrow amount in database: %q", escrow.Amount)), nil, nil
	}

	escrowAddr := common.HexToAddress(escrow.EscrowAddress)
	isERC20 := escrow.Token != "" && escrow.Token != "0x0000000000000000000000000000000000000000"

	if isERC20 {
		tokenAddr := common.HexToAddress(escrow.Token)
		approveTx, err := s.chain.ApproveERC20(ctx, tokenAddr, escrowAddr, amount)
		if err != nil {
			return textResult(fmt.Sprintf("approve error: %v", err)), nil, nil
		}
		approveReceipt, err := chain.WaitMined(ctx, s.chain, approveTx.Hash())
		if err != nil {
			return textResult(fmt.Sprintf("approve receipt error: %v", err)), nil, nil
		}
		if approveReceipt.Status != 1 {
			return textResult("approve transaction reverted"), nil, nil
		}
		tx, err := s.chain.Fund(ctx, escrowAddr, nil)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	}

	tx, err := s.chain.Fund(ctx, escrowAddr, amount)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) handleDepositStake(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	stakeAmount, ok := new(big.Int).SetString(escrow.WorkerStake, 10)
	if !ok || stakeAmount.Sign() <= 0 {
		return textResult("this escrow does not require a worker stake"), nil, nil
	}

	escrowAddr := common.HexToAddress(escrow.EscrowAddress)
	isERC20 := escrow.Token != "" && escrow.Token != "0x0000000000000000000000000000000000000000"

	if isERC20 {
		tokenAddr := common.HexToAddress(escrow.Token)
		approveTx, err := s.chain.ApproveERC20(ctx, tokenAddr, escrowAddr, stakeAmount)
		if err != nil {
			return textResult(fmt.Sprintf("approve error: %v", err)), nil, nil
		}
		approveReceipt, err := chain.WaitMined(ctx, s.chain, approveTx.Hash())
		if err != nil {
			return textResult(fmt.Sprintf("approve receipt error: %v", err)), nil, nil
		}
		if approveReceipt.Status != 1 {
			return textResult("approve transaction reverted"), nil, nil
		}
		tx, err := s.chain.DepositStake(ctx, escrowAddr, nil)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	}

	tx, err := s.chain.DepositStake(ctx, escrowAddr, stakeAmount)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) handleSubmitWork(ctx context.Context, req *mcp.CallToolRequest, args submitArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	hash := crypto.Keccak256Hash([]byte(args.SubmissionURI))
	var hashBytes [32]byte
	copy(hashBytes[:], hash.Bytes())

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex)
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		tx, err := s.chain.SubmitMilestone(ctx, addr, msIdx, hashBytes, args.SubmissionURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	}

	tx, err := s.chain.Submit(ctx, addr, hashBytes, args.SubmissionURI)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) handleApproveWork(ctx context.Context, req *mcp.CallToolRequest, args approveArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex)
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		switch args.Role {
		case "buyer":
			tx, err := s.chain.ApproveMilestoneByBuyer(ctx, addr, msIdx)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
		case "verifier":
			tx, err := s.chain.ApproveMilestoneByVerifier(ctx, addr, msIdx)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
		default:
			return textResult("role must be 'buyer' or 'verifier'"), nil, nil
		}
	}

	switch args.Role {
	case "buyer":
		tx, err := s.chain.ApproveByBuyer(ctx, addr)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	case "verifier":
		tx, err := s.chain.ApproveByVerifier(ctx, addr)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	default:
		return textResult("role must be 'buyer' or 'verifier'"), nil, nil
	}
}

func (s *Server) handleDisputeWork(ctx context.Context, req *mcp.CallToolRequest, args disputeArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex)
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		switch args.Role {
		case "buyer":
			tx, err := s.chain.DisputeMilestone(ctx, addr, msIdx, args.ReasonURI)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
		case "verifier":
			tx, err := s.chain.RejectMilestoneByVerifier(ctx, addr, msIdx, args.ReasonURI)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
		case "worker":
			tx, err := s.chain.EscalateMilestoneSilence(ctx, addr, msIdx, args.ReasonURI)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
		default:
			return textResult("role must be 'buyer', 'verifier', or 'worker'"), nil, nil
		}
	}

	switch args.Role {
	case "buyer":
		tx, err := s.chain.Dispute(ctx, addr, args.ReasonURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	case "verifier":
		tx, err := s.chain.RejectByVerifier(ctx, addr, args.ReasonURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	case "worker":
		tx, err := s.chain.EscalateSilence(ctx, addr, args.ReasonURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	default:
		return textResult("role must be 'buyer', 'verifier', or 'worker'"), nil, nil
	}
}

func (s *Server) handleResolveDispute(ctx context.Context, req *mcp.CallToolRequest, args resolveArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	bps, err := strconv.ParseUint(args.WorkerAwardBps, 10, 16)
	if err != nil {
		return textResult(fmt.Sprintf("invalid worker_award_bps: %v", err)), nil, nil
	}
	if bps > 10_000 {
		return textResult("worker_award_bps must be between 0 and 10000"), nil, nil
	}

	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex)
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		tx, err := s.chain.ResolveMilestoneDispute(ctx, addr, msIdx, uint16(bps), args.ResolutionURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
	}

	tx, err := s.chain.ResolveDispute(ctx, addr, uint16(bps), args.ResolutionURI)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) handleGetEscrow(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	result := map[string]any{"escrow": escrow}

	if escrow.MilestoneCount > 1 {
		milestones, err := s.db.GetMilestonesByEscrow(escrowID)
		if err != nil {
			return textResult(fmt.Sprintf("failed to fetch milestones for escrow %d: %v", escrowID, err)), nil, nil
		}
		result["milestones"] = milestones
	}

	return jsonResult(result)
}

func (s *Server) handleListEscrows(ctx context.Context, req *mcp.CallToolRequest, args listArgs) (*mcp.CallToolResult, any, error) {
	escrows, err := s.db.ListEscrows(args.Role, args.Address, args.Status)
	if err != nil {
		return textResult(fmt.Sprintf("error: %v", err)), nil, nil
	}
	return jsonResult(escrows)
}

func (s *Server) handleAbortRemainingMilestones(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}
	if escrow.MilestoneCount <= 1 {
		return textResult("abort_remaining_milestones is only available for multi-milestone escrows"), nil, nil
	}

	tx, err := s.chain.AbortRemainingMilestones(ctx, common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) handleActivateBackup(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	escrow, err := s.db.GetEscrow(escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	if escrow.BackupWorker == "" || escrow.BackupWorker == "0x0000000000000000000000000000000000000000" {
		return textResult("this escrow has no backup worker designated"), nil, nil
	}
	if escrow.BackupActivated {
		return textResult("backup already active"), nil, nil
	}

	tx, err := s.chain.ActivateBackup(ctx, common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) handleGetReputation(ctx context.Context, req *mcp.CallToolRequest, args reputationArgs) (*mcp.CallToolResult, any, error) {
	if !common.IsHexAddress(args.Address) {
		return textResult("invalid address"), nil, nil
	}
	addr := strings.ToLower(common.HexToAddress(args.Address).Hex())

	if args.Role != "" && args.Role != "worker" && args.Role != "buyer" {
		return textResult("role must be 'worker', 'buyer', or omitted"), nil, nil
	}

	if args.Role != "" {
		rep, err := s.db.GetReputation(addr, args.Role)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return jsonResult(map[string]any{
					"address":   addr,
					"role":      args.Role,
					"completed": 0,
					"disputed":  0,
					"failed":    0,
				})
			}
			return nil, nil, fmt.Errorf("get reputation: %w", err)
		}
		return jsonResult(rep)
	}

	reps, err := s.db.GetReputationByAddress(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("get reputation by address: %w", err)
	}
	if len(reps) == 0 {
		return jsonResult([]map[string]any{
			{"address": addr, "role": "worker", "completed": 0, "disputed": 0, "failed": 0},
			{"address": addr, "role": "buyer", "completed": 0, "disputed": 0, "failed": 0},
		})
	}
	return jsonResult(reps)
}

func (s *Server) biddingService() *bidding.Service {
	return &bidding.Service{
		DB:    s.db,
		Chain: s.chain,
		Idx:   s.idx,
		Cfg:   s.cfg,
	}
}

func (s *Server) handleCreateRFQ(ctx context.Context, req *mcp.CallToolRequest, args createRFQArgs) (*mcp.CallToolResult, any, error) {
	deadline, err := strconv.ParseInt(args.Deadline, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid deadline: %v", err)), nil, nil
	}
	review, err := strconv.ParseInt(args.ReviewPeriodSeconds, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid review_period_seconds: %v", err)), nil, nil
	}
	dispute, err := strconv.ParseInt(args.DisputePeriodSeconds, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid dispute_period_seconds: %v", err)), nil, nil
	}
	arbTimeout, err := strconv.ParseInt(args.ArbitratorTimeoutSeconds, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid arbitrator_timeout_seconds: %v", err)), nil, nil
	}
	expiresAt, err := strconv.ParseInt(args.ExpiresAt, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid expires_at: %v", err)), nil, nil
	}

	svc := s.biddingService()
	rfq, err := svc.CreateRFQ(bidding.CreateRFQParams{
		Title:                    args.Title,
		Description:              args.Description,
		Buyer:                    args.Buyer,
		Token:                    args.Token,
		BudgetMin:                args.BudgetMin,
		BudgetMax:                args.BudgetMax,
		Deadline:                 deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		ArbitratorTimeoutSeconds: arbTimeout,
		Verifier:                 args.Verifier,
		Arbitrator:               args.Arbitrator,
		WorkerStake:              args.WorkerStake,
		MilestonesJSON:           args.MilestonesJSON,
		RequirementsJSON:         args.RequirementsJSON,
		ExpiresAt:                expiresAt,
	})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}

	return jsonResult(map[string]any{
		"rfq_id": rfq.ID,
		"status": rfq.Status,
	})
}

func (s *Server) handlePlaceBid(ctx context.Context, req *mcp.CallToolRequest, args placeBidArgs) (*mcp.CallToolResult, any, error) {
	rfqID, err := strconv.ParseInt(args.RFQID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid rfq_id: %v", err)), nil, nil
	}
	var estimatedDuration int64
	if args.EstimatedDuration != "" {
		estimatedDuration, err = strconv.ParseInt(args.EstimatedDuration, 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid estimated_duration: %v", err)), nil, nil
		}
	}
	expiresAt, err := strconv.ParseInt(args.ExpiresAt, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid expires_at: %v", err)), nil, nil
	}

	svc := s.biddingService()
	bid, err := svc.PlaceBid(bidding.PlaceBidParams{
		RFQID:             rfqID,
		Bidder:            args.Bidder,
		Amount:            args.Amount,
		EstimatedDuration: estimatedDuration,
		ReputationBond:    args.ReputationBond,
		MilestonesJSON:    args.MilestonesJSON,
		Message:           args.Message,
		ExpiresAt:         expiresAt,
		StakeMandateID:    args.StakeMandateID,
	})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}

	return jsonResult(map[string]any{
		"bid_id": bid.ID,
		"status": bid.Status,
	})
}

func (s *Server) handleListBids(ctx context.Context, req *mcp.CallToolRequest, args listBidsArgs) (*mcp.CallToolResult, any, error) {
	if args.RFQID != "" {
		rfqID, err := strconv.ParseInt(args.RFQID, 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid rfq_id: %v", err)), nil, nil
		}
		bids, err := s.db.ListBidsByRFQ(rfqID)
		if err != nil {
			return textResult(fmt.Sprintf("error: %v", err)), nil, nil
		}
		return jsonResult(bids)
	}
	if args.Bidder != "" {
		bids, err := s.db.ListBidsByBidder(args.Bidder)
		if err != nil {
			return textResult(fmt.Sprintf("error: %v", err)), nil, nil
		}
		return jsonResult(bids)
	}
	return textResult("provide either rfq_id or bidder"), nil, nil
}

func (s *Server) handleAcceptBid(ctx context.Context, req *mcp.CallToolRequest, args acceptBidArgs) (*mcp.CallToolResult, any, error) {
	rfqID, err := strconv.ParseInt(args.RFQID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid rfq_id: %v", err)), nil, nil
	}
	bidID, err := strconv.ParseInt(args.BidID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid bid_id: %v", err)), nil, nil
	}

	svc := s.biddingService()
	result, err := svc.AcceptBid(ctx, bidding.AcceptBidParams{
		RFQID:  rfqID,
		BidID:  bidID,
		Caller: args.Caller,
	})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}

	return jsonResult(map[string]any{
		"escrow_id":       result.Escrow.ID,
		"task_id":         result.Task.ID,
		"tx_hash":         result.TxHash,
		"escrow_address":  result.Escrow.EscrowAddress,
		"chain_escrow_id": result.Escrow.EscrowID,
		"bid_id":          result.Bid.ID,
		"bid_status":      result.Bid.Status,
	})
}

func (s *Server) handleFundViaMandate(ctx context.Context, req *mcp.CallToolRequest, args fundViaMandateArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID, 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}

	v, err := strconv.ParseUint(args.AuthV, 10, 8)
	if err != nil {
		return textResult(fmt.Sprintf("invalid auth_v: %v", err)), nil, nil
	}

	payload := map[string]any{}
	if args.Payload != "" {
		if err := json.Unmarshal([]byte(args.Payload), &payload); err != nil {
			return textResult(fmt.Sprintf("invalid payload JSON: %v", err)), nil, nil
		}
	}

	authTo := args.AuthTo
	if authTo == "" {
		escrow, err := s.db.GetEscrow(escrowID)
		if err != nil {
			return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
		}
		authTo = escrow.EscrowAddress
	}

	env := ap2.MandateEnvelope{
		Type:          ap2.MandateType(args.MandateType),
		Payload:       payload,
		Signature:     args.Signature,
		SignerAddress: args.SignerAddress,
		Authorization: ap2.EIP3009Authorization{
			From:        args.AuthFrom,
			To:          authTo,
			Value:       args.AuthValue,
			ValidAfter:  args.AuthValidAfter,
			ValidBefore: args.AuthValidBefore,
			Nonce:       args.AuthNonce,
			V:           uint8(v),
			R:           args.AuthR,
			S:           args.AuthS,
		},
	}

	svc := &ap2.Service{
		DB:    s.db,
		Chain: s.chain,
		Idx:   s.idx,
		Cfg:   s.cfg,
	}

	resp, err := svc.FundViaMandate(ctx, escrowID, env)
	if err != nil {
		return textResult(fmt.Sprintf("fund via mandate error: %v", err)), nil, nil
	}

	return jsonResult(resp)
}

func (s *Server) handleGetAgentCard(ctx context.Context, req *mcp.CallToolRequest, args emptyArgs) (*mcp.CallToolResult, any, error) {
	svc := &a2a.Service{
		DB:    s.db,
		Chain: s.chain,
		Idx:   s.idx,
		Cfg:   s.cfg,
	}
	card := svc.BuildAgentCard()
	return jsonResult(card)
}

func (s *Server) handleSubscribeEvents(ctx context.Context, req *mcp.CallToolRequest, args subscribeEventsArgs) (*mcp.CallToolResult, any, error) {
	granularity := events.ParseGranularity(args.Granularity)

	limit := 50
	if args.Limit != "" {
		v, err := strconv.Atoi(args.Limit)
		if err != nil || v <= 0 {
			return textResult("invalid limit: must be a positive integer"), nil, nil
		}
		if v > 200 {
			v = 200
		}
		limit = v
	}

	recent := s.bus.RecentEvents(args.EscrowID, granularity, args.SinceID, limit)

	type eventOut struct {
		Event     string         `json:"event"`
		Escrow    string         `json:"escrow,omitempty"`
		ID        string         `json:"id"`
		Block     uint64         `json:"block,omitempty"`
		Timestamp int64          `json:"timestamp"`
		Payload   map[string]any `json:"payload,omitempty"`
	}

	out := make([]eventOut, len(recent))
	for i, e := range recent {
		out[i] = eventOut{
			Event:     e.Name,
			Escrow:    e.Escrow,
			ID:        e.ID,
			Block:     e.Block,
			Timestamp: e.Timestamp.Unix(),
			Payload:   e.Payload,
		}
	}

	var cursor string
	if len(recent) > 0 {
		cursor = recent[len(recent)-1].ID
	}

	return jsonResult(map[string]any{
		"events": out,
		"cursor": cursor,
		"count":  len(out),
	})
}

func parseMilestoneIndex(s string) (uint8, error) {
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("invalid milestone_index: %v", err)
	}
	return uint8(v), nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textResult(fmt.Sprintf("json error: %v", err)), nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}
