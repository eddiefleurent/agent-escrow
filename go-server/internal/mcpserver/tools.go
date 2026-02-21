package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createEscrowArgs struct {
	Title                    string `json:"title" jsonschema:"Task title"`
	Description              string `json:"description" jsonschema:"Task description"`
	Buyer                    string `json:"buyer" jsonschema:"Buyer address"`
	Worker                   string `json:"worker" jsonschema:"Worker address"`
	Verifier                 string `json:"verifier" jsonschema:"Verifier address"`
	Arbitrator               string `json:"arbitrator" jsonschema:"Arbitrator address"`
	Amount                   string `json:"amount" jsonschema:"Amount in wei (ETH) or smallest unit (ERC20)"`
	SubmissionDeadline       string `json:"submission_deadline" jsonschema:"Unix timestamp for submission deadline"`
	ReviewPeriodSeconds      string `json:"review_period_seconds" jsonschema:"Review period in seconds"`
	DisputePeriodSeconds     string `json:"dispute_period_seconds" jsonschema:"Dispute period in seconds"`
	ArbitratorTimeoutSeconds string `json:"arbitrator_timeout_seconds" jsonschema:"Arbitrator timeout in seconds"`
	Token                    string `json:"token,omitempty" jsonschema:"ERC20 token address; omit or use 0x0000000000000000000000000000000000000000 for ETH"`
}

type escrowIDArgs struct {
	EscrowID string `json:"escrow_id" jsonschema:"Database escrow ID"`
}

type submitArgs struct {
	EscrowID      string `json:"escrow_id" jsonschema:"Database escrow ID"`
	SubmissionURI string `json:"submission_uri" jsonschema:"URI of submission"`
}

type approveArgs struct {
	EscrowID string `json:"escrow_id" jsonschema:"Database escrow ID"`
	Role     string `json:"role" jsonschema:"Role: buyer or verifier"`
}

type disputeArgs struct {
	EscrowID  string `json:"escrow_id" jsonschema:"Database escrow ID"`
	Role      string `json:"role" jsonschema:"Role: buyer, verifier, or worker"`
	ReasonURI string `json:"reason_uri" jsonschema:"URI describing reason"`
}

type resolveArgs struct {
	EscrowID       string `json:"escrow_id" jsonschema:"Database escrow ID"`
	WorkerAwardBps string `json:"worker_award_bps" jsonschema:"Worker award basis points 0-10000"`
	ResolutionURI  string `json:"resolution_uri" jsonschema:"URI of resolution"`
}

type listArgs struct {
	Role    string `json:"role" jsonschema:"Filter by role"`
	Address string `json:"address" jsonschema:"Address for role filter"`
	Status  string `json:"status" jsonschema:"Filter by status"`
}

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
}

func (s *Server) handleCreateEscrow(ctx context.Context, req *mcp.CallToolRequest, args createEscrowArgs) (*mcp.CallToolResult, any, error) {
	amount, ok := new(big.Int).SetString(args.Amount, 10)
	if !ok {
		return textResult("invalid amount"), nil, nil
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

	specHash := crypto.Keccak256Hash([]byte(args.Title + args.Description))

	var tokenAddr common.Address
	if args.Token != "" {
		if !common.IsHexAddress(args.Token) {
			return textResult("invalid token address"), nil, nil
		}
		tokenAddr = common.HexToAddress(args.Token)
	}

	factory := common.HexToAddress(s.cfg.FactoryAddress)
	params := chain.CreateEscrowParams{
		Buyer:                    common.HexToAddress(args.Buyer),
		Worker:                   common.HexToAddress(args.Worker),
		Verifier:                 common.HexToAddress(args.Verifier),
		Arbitrator:               common.HexToAddress(args.Arbitrator),
		Amount:                   amount,
		SubmissionDeadline:       deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		TaskSpecHash:             specHash,
		ArbitratorTimeoutSeconds: arbTimeout,
		Token:                    tokenAddr,
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
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       int64(deadline),
		ReviewPeriodSeconds:      int64(review),
		DisputePeriodSeconds:     int64(dispute),
		ArbitratorTimeoutSeconds: int64(arbTimeout),
	})
	if err != nil {
		return textResult(fmt.Sprintf("db error: %v", err)), nil, nil
	}

	_ = s.idx.RunOnce(ctx)

	return jsonResult(map[string]any{
		"escrow_id":       escrow.ID,
		"tx_hash":         tx.Hash().Hex(),
		"task_id":         task.ID,
		"escrow_address":  result.EscrowAddress.Hex(),
		"chain_escrow_id": result.EscrowID,
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

	tx, err := s.chain.Submit(ctx, common.HexToAddress(escrow.EscrowAddress), hashBytes, args.SubmissionURI)
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

	tx, err := s.chain.ResolveDispute(ctx, common.HexToAddress(escrow.EscrowAddress), uint16(bps), args.ResolutionURI)
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
	return jsonResult(escrow)
}

func (s *Server) handleListEscrows(ctx context.Context, req *mcp.CallToolRequest, args listArgs) (*mcp.CallToolResult, any, error) {
	escrows, err := s.db.ListEscrows(args.Role, args.Address, args.Status)
	if err != nil {
		return textResult(fmt.Sprintf("error: %v", err)), nil, nil
	}
	return jsonResult(escrows)
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
