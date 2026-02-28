package mcpserver

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/a2a"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/ap2"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/attestation"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/bidding"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/dct"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/events"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/numconv"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type milestoneArg struct {
	Amount             FlexibleString `json:"amount" jsonschema:"Milestone amount in wei or smallest unit"`
	SubmissionDeadline FlexibleString `json:"submission_deadline" jsonschema:"Unix timestamp for milestone submission deadline"`
}

type createEscrowArgs struct {
	Title                    string         `json:"title" jsonschema:"Task title"`
	Description              string         `json:"description" jsonschema:"Task description"`
	Buyer                    string         `json:"buyer" jsonschema:"Buyer address (0x...)"`
	Worker                   string         `json:"worker" jsonschema:"Worker address (0x...). Must be distinct from buyer, verifier, and arbitrator."`
	Verifier                 string         `json:"verifier" jsonschema:"Verifier address (0x...). Must be distinct from buyer, worker, and arbitrator."`
	Arbitrator               string         `json:"arbitrator" jsonschema:"Arbitrator address (0x...). Must be distinct from buyer, worker, and verifier."`
	Amount                   FlexibleString `json:"amount" jsonschema:"Total amount in wei (ETH) or smallest unit (ERC20)"`
	WorkerStake              FlexibleString `json:"worker_stake,omitempty" jsonschema:"Worker anti-Sybil stake in wei or smallest unit; omit or 0 for no stake"`
	SubmissionDeadline       FlexibleString `json:"submission_deadline" jsonschema:"Unix timestamp for submission deadline"`
	ReviewPeriodSeconds      FlexibleString `json:"review_period_seconds" jsonschema:"Review period in seconds"`
	DisputePeriodSeconds     FlexibleString `json:"dispute_period_seconds" jsonschema:"Dispute period in seconds"`
	ArbitratorTimeoutSeconds FlexibleString `json:"arbitrator_timeout_seconds" jsonschema:"Arbitrator timeout in seconds"`
	Token                    string         `json:"token,omitempty" jsonschema:"Omit or leave empty for ETH. ERC20 token address otherwise."`
	ServiceTier              FlexibleString `json:"service_tier,omitempty" jsonschema:"0 = low_assurance (optimistic, default), 1 = high_assurance (verifier approval required). Paper §5.3."`
	Milestones               []milestoneArg `json:"milestones,omitempty" jsonschema:"Optional array of milestones; omit for single-milestone (V1) escrow"`
	BackupWorker             string         `json:"backup_worker,omitempty" jsonschema:"Optional backup worker address; omit for no backup agent"`
	BackupDeadlineExtension  FlexibleString `json:"backup_deadline_extension,omitempty" jsonschema:"Seconds to extend deadline when backup activates; omit or 0 for no extension"`
	ZKVerifier               string         `json:"zk_verifier,omitempty" jsonschema:"Optional on-chain ZK verifier contract address; required with circuit_id"`
	CircuitID                string         `json:"circuit_id,omitempty" jsonschema:"Optional 0x-prefixed bytes32 circuit identifier; required with zk_verifier"`
}

type escrowIDArgs struct {
	EscrowID FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID (from create_escrow response), or on-chain escrow address (0x...)"`
}

type submitArgs struct {
	EscrowID             FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	SubmissionURI        string         `json:"submission_uri" jsonschema:"URI of submission"`
	ProofHash            string         `json:"proof_hash,omitempty" jsonschema:"Optional 0x-prefixed bytes32 commitment hash of the external ZK proof"`
	MilestoneIndex       FlexibleString `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
	AttestationChainJSON string         `json:"attestation_chain_json,omitempty" jsonschema:"JSON array of completion-attestation-v1 payloads for delegation chain verification (paper §4.8). Required when escrow has sub-delegated child escrows."`
}

type verifyAndApproveArgs struct {
	EscrowID       FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	Proof          string         `json:"proof" jsonschema:"0x-prefixed ABI payload for the zk proof bytes"`
	MilestoneIndex FlexibleString `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type getAttestationChainArgs struct {
	EscrowID FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
}

type approveArgs struct {
	EscrowID       FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	Role           string         `json:"role" jsonschema:"Role: buyer or verifier"`
	MilestoneIndex FlexibleString `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type disputeArgs struct {
	EscrowID       FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	Role           string         `json:"role" jsonschema:"Role: buyer, verifier, or worker"`
	ReasonURI      string         `json:"reason_uri" jsonschema:"URI describing reason"`
	MilestoneIndex FlexibleString `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type resolveArgs struct {
	EscrowID       FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	WorkerAwardBps FlexibleString `json:"worker_award_bps" jsonschema:"Worker award basis points 0-10000"`
	ResolutionURI  string         `json:"resolution_uri" jsonschema:"URI of resolution"`
	MilestoneIndex FlexibleString `json:"milestone_index,omitempty" jsonschema:"Milestone index (required for multi-milestone escrows)"`
}

type listArgs struct {
	Role    string `json:"role" jsonschema:"Filter by role"`
	Address string `json:"address" jsonschema:"Address for role filter"`
	Status  string `json:"status" jsonschema:"Filter by status"`
}

type createRFQArgs struct {
	Title                    string         `json:"title" jsonschema:"Task title for the RFQ"`
	Description              string         `json:"description" jsonschema:"Task description for the RFQ"`
	Buyer                    string         `json:"buyer" jsonschema:"Buyer address broadcasting the RFQ"`
	Token                    string         `json:"token,omitempty" jsonschema:"Omit or leave empty for ETH. ERC20 token address otherwise."`
	BudgetMin                FlexibleString `json:"budget_min" jsonschema:"Minimum budget in wei or smallest unit"`
	BudgetMax                FlexibleString `json:"budget_max" jsonschema:"Maximum budget in wei or smallest unit"`
	Deadline                 FlexibleString `json:"deadline" jsonschema:"Unix timestamp: task submission deadline (when work must be done by)"`
	ReviewPeriodSeconds      FlexibleString `json:"review_period_seconds" jsonschema:"Review period in seconds"`
	DisputePeriodSeconds     FlexibleString `json:"dispute_period_seconds" jsonschema:"Dispute period in seconds"`
	ArbitratorTimeoutSeconds FlexibleString `json:"arbitrator_timeout_seconds" jsonschema:"Arbitrator timeout in seconds"`
	Verifier                 string         `json:"verifier,omitempty" jsonschema:"Designated verifier address; omit if unknown at RFQ time"`
	Arbitrator               string         `json:"arbitrator,omitempty" jsonschema:"Designated arbitrator address; omit if unknown at RFQ time"`
	WorkerStake              FlexibleString `json:"worker_stake,omitempty" jsonschema:"Required worker stake; omit or 0 for none"`
	MilestonesJSON           string         `json:"milestones_json,omitempty" jsonschema:"JSON array of milestone specs"`
	RequirementsJSON         string         `json:"requirements_json,omitempty" jsonschema:"JSON: capability requirements, tags, constraints"`
	RequiredProofProtocol    string         `json:"required_proof_protocol,omitempty" jsonschema:"Optional required ZK proof protocol for this RFQ (currently: 'groth16')"`
	RequiredCredentialsJSON  string         `json:"required_credentials_json,omitempty" jsonschema:"JSON array of credential requirement selectors [{domain, capabilities, trusted_issuers}]. Bidders must present matching attestations."`
	CommitDeadline           FlexibleString `json:"commit_deadline,omitempty" jsonschema:"Unix timestamp: end of commit phase (sealed bids)"`
	RevealDeadline           FlexibleString `json:"reveal_deadline,omitempty" jsonschema:"Unix timestamp: end of reveal phase (must be >= commit_deadline and <= deadline)"`
	ServiceTier              FlexibleString `json:"service_tier,omitempty" jsonschema:"0 = low_assurance (optimistic, default), 1 = high_assurance (verifier approval required). Paper §5.3."`
	ExpiresAt                FlexibleString `json:"expires_at" jsonschema:"Unix timestamp: when the RFQ stops accepting bids (distinct from deadline which is when work must be done)"`
	ParentEscrowID           FlexibleString `json:"parent_escrow_id,omitempty" jsonschema:"Optional parent escrow ID for sub-delegation (paper §4.8). Buyer must be active worker of parent."`
}

type commitBidArgs struct {
	RFQID      FlexibleString `json:"rfq_id" jsonschema:"RFQ ID to commit on"`
	Bidder     string         `json:"bidder" jsonschema:"Worker agent address placing the commit"`
	Commitment string         `json:"commitment" jsonschema:"0x-prefixed keccak256 commitment hash"`
	Nonce      string         `json:"nonce" jsonschema:"Bidder nonce used in commitment preimage"`
}

type revealBidArgs struct {
	RFQID             FlexibleString `json:"rfq_id" jsonschema:"RFQ ID to bid on"`
	Bidder            string         `json:"bidder" jsonschema:"Worker agent address placing the bid"`
	Nonce             string         `json:"nonce" jsonschema:"Nonce used in prior commit"`
	Salt              string         `json:"salt" jsonschema:"Secret salt used in commitment preimage"`
	Amount            FlexibleString `json:"amount" jsonschema:"Proposed total price in wei or smallest unit"`
	EstimatedDuration FlexibleString `json:"estimated_duration,omitempty" jsonschema:"Estimated seconds to complete"`
	ReputationBond    FlexibleString `json:"reputation_bond,omitempty" jsonschema:"Offered reputation bond in wei"`
	MilestonesJSON    string         `json:"milestones_json,omitempty" jsonschema:"JSON: proposed milestone breakdown"`
	Message           string         `json:"message,omitempty" jsonschema:"Free-form bid justification"`
	ExpiresAt         FlexibleString `json:"expires_at,omitempty" jsonschema:"Unix timestamp: bid expiry (must not exceed RFQ deadline)"`
	StakeMandateID    string         `json:"stake_mandate_id,omitempty" jsonschema:"Optional AP2 mandate ID for Sybil-resistant stake-on-bid (paper §6)"`
	CredentialsJSON   string         `json:"credentials_json,omitempty" jsonschema:"JSON array of attestation-v1 payloads proving bidder capabilities (paper §4.6 Table 3)"`
}

type listBidsArgs struct {
	RFQID  FlexibleString `json:"rfq_id,omitempty" jsonschema:"List bids for this RFQ ID"`
	Bidder string         `json:"bidder,omitempty" jsonschema:"List bids by this bidder address"`
}

type acceptBidArgs struct {
	RFQID  FlexibleString `json:"rfq_id" jsonschema:"RFQ ID"`
	BidID  FlexibleString `json:"bid_id" jsonschema:"Bid ID to accept. WARNING: accepting a bid immediately deploys an escrow on-chain (irreversible). The next step is to fund the new escrow."`
	Caller string         `json:"caller,omitempty" jsonschema:"Caller address (must match RFQ buyer)"`
}

type reputationArgs struct {
	Address string `json:"address" jsonschema:"Ethereum address to look up reputation for"`
	Role    string `json:"role,omitempty" jsonschema:"Optional: 'worker' or 'buyer'. Omit to return both roles."`
}

type mintDCTArgs struct {
	EscrowID   FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID"`
	Subject    string         `json:"subject" jsonschema:"Delegatee subject address or identifier"`
	Issuer     string         `json:"issuer,omitempty" jsonschema:"Issuer identifier (optional)"`
	Operations []string       `json:"operations" jsonschema:"Allowed operations, e.g. ['submit_work']"`
	Resources  []string       `json:"resources" jsonschema:"Allowed resources, e.g. ['escrow:12']"`
	ExpiresAt  FlexibleString `json:"expires_at" jsonschema:"Unix timestamp expiry"`
	Caller     string         `json:"caller" jsonschema:"Caller address (0x...) for authorization"`
}

type delegateDCTArgs struct {
	ParentToken string         `json:"parent_token" jsonschema:"Parent DCT token string"`
	Subject     string         `json:"subject" jsonschema:"Delegatee subject"`
	Issuer      string         `json:"issuer,omitempty" jsonschema:"Issuer identifier (optional)"`
	Operations  []string       `json:"operations" jsonschema:"Subset of parent operations"`
	Resources   []string       `json:"resources" jsonschema:"Subset of parent resources"`
	ExpiresAt   FlexibleString `json:"expires_at" jsonschema:"Unix timestamp <= parent expiry"`
	Caller      string         `json:"caller" jsonschema:"Caller address (0x...) for authorization"`
}

type introspectDCTArgs struct {
	Token string `json:"token" jsonschema:"DCT token string to introspect"`
}

type revokeDCTArgs struct {
	TokenID string `json:"token_id" jsonschema:"DCT token ID (dct_...) to revoke"`
	Reason  string `json:"reason,omitempty" jsonschema:"Optional revocation reason"`
	By      string `json:"by,omitempty" jsonschema:"Revoker identity"`
	Caller  string `json:"caller" jsonschema:"Caller address (0x...) for authorization"`
}

type emergencyOverrideDCTArgs struct {
	EscrowID      FlexibleString `json:"escrow_id" jsonschema:"Escrow ID for the override"`
	Operation     string         `json:"operation" jsonschema:"Operation to override (e.g. 'revoke_all')"`
	CallerAddress string         `json:"caller_address" jsonschema:"Target caller address for the override"`
	Reason        string         `json:"reason" jsonschema:"Reason for the emergency override"`
	Owner         string         `json:"owner" jsonschema:"Factory owner address authorizing the override"`
}

type listDCTAuditArgs struct {
	EscrowID FlexibleString `json:"escrow_id,omitempty" jsonschema:"Filter by escrow ID (optional)"`
	Limit    FlexibleString `json:"limit,omitempty" jsonschema:"Max results (default 50)"`
	Offset   FlexibleString `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

type fundViaMandateArgs struct {
	EscrowID        FlexibleString `json:"escrow_id" jsonschema:"Database escrow ID to fund"`
	MandateType     string         `json:"mandate_type" jsonschema:"AP2 mandate type: intent, cart, or payment"`
	SignerAddress   string         `json:"signer_address" jsonschema:"Mandate signer address (must be escrow buyer)"`
	Signature       string         `json:"signature" jsonschema:"Cryptographic signature of the mandate"`
	Payload         string         `json:"payload,omitempty" jsonschema:"JSON string of mandate payload (budget, items, etc.)"`
	AuthFrom        string         `json:"auth_from" jsonschema:"EIP-3009 authorization from address"`
	AuthTo          string         `json:"auth_to" jsonschema:"EIP-3009 authorization to address (must match escrow address)"`
	AuthValue       string         `json:"auth_value" jsonschema:"EIP-3009 authorization value"`
	AuthValidAfter  string         `json:"auth_valid_after" jsonschema:"EIP-3009 validAfter timestamp"`
	AuthValidBefore string         `json:"auth_valid_before" jsonschema:"EIP-3009 validBefore timestamp"`
	AuthNonce       string         `json:"auth_nonce" jsonschema:"EIP-3009 nonce (hex)"`
	AuthV           FlexibleString `json:"auth_v" jsonschema:"EIP-3009 signature v"`
	AuthR           string         `json:"auth_r" jsonschema:"EIP-3009 signature r (hex)"`
	AuthS           string         `json:"auth_s" jsonschema:"EIP-3009 signature s (hex)"`
}

type subscribeEventsArgs struct {
	EscrowAddress string         `json:"escrow_address,omitempty" jsonschema:"Filter to specific escrow address; omit for all"`
	SinceID       string         `json:"since_id,omitempty" jsonschema:"Return events after this event ID (cursor-based pagination)"`
	Granularity   string         `json:"granularity,omitempty" jsonschema:"Granularity level: L0 (heartbeats + all events), L1 (all events, no heartbeats, default), L2 (state changes only), L3 (terminal events only)"`
	Limit         FlexibleString `json:"limit,omitempty" jsonschema:"Max events to return (default 50, max 200)"`
}

type commitCheckpointArgs struct {
	EscrowID         FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	StateSnapshotURI string         `json:"state_snapshot_uri" jsonschema:"URI pointing to the checkpoint state snapshot artifact"`
	SnapshotHash     string         `json:"snapshot_hash,omitempty" jsonschema:"Optional content hash of the snapshot for integrity verification"`
	SchemaVersion    string         `json:"schema_version,omitempty" jsonschema:"Checkpoint schema version (default: checkpoint-v1)"`
	CommittedBy      string         `json:"committed_by" jsonschema:"Address of the active worker committing the checkpoint (must match escrow's active_worker)"`
	MilestoneIndex   FlexibleString `json:"milestone_index,omitempty" jsonschema:"Milestone index (0-based) for multi-milestone escrows"`
	CompletionPct    FlexibleString `json:"completion_pct,omitempty" jsonschema:"Estimated completion percentage 0-100"`
	MetadataJSON     string         `json:"metadata_json,omitempty" jsonschema:"Optional JSON metadata (tool versions, environment info, etc.)"`
}

type listCheckpointsArgs struct {
	EscrowID       FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	MilestoneIndex FlexibleString `json:"milestone_index,omitempty" jsonschema:"Optional milestone index filter (0-based)"`
}

type getLatestCheckpointArgs struct {
	EscrowID       FlexibleString `json:"escrow_id" jsonschema:"Numeric database escrow ID or on-chain escrow address (0x...)"`
	MilestoneIndex FlexibleString `json:"milestone_index,omitempty" jsonschema:"Optional milestone index filter (0-based)"`
}

type emptyArgs struct{}

type addressArgs struct {
	Address string `json:"address" jsonschema:"Ethereum address to freeze/unfreeze"`
}

type emergencyResolveArgs struct {
	EscrowID       FlexibleString `json:"escrow_id" jsonschema:"Database escrow ID"`
	WorkerAwardBps FlexibleString `json:"worker_award_bps" jsonschema:"Worker award basis points 0-10000"`
}

type emergencyListArgs struct {
	Limit  FlexibleString `json:"limit,omitempty" jsonschema:"Max results to return (default 50)"`
	Offset FlexibleString `json:"offset,omitempty" jsonschema:"Offset for pagination (default 0)"`
}

func (s *Server) registerTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_escrow",
		Description: "Create a new task escrow contract via the factory. All amounts (amount, worker_stake, milestone amounts) are in wei for ETH or the smallest token unit for ERC20. All four roles (buyer, worker, verifier, arbitrator) must be distinct addresses. Returns escrow_id for subsequent calls.",
	}, s.handleCreateEscrow)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fund_escrow",
		Description: "Fund an escrow as the buyer. For ETH escrows, sends the exact amount on-chain. For ERC20 escrows, automatically approves and transfers the token. After funding, call deposit_stake if the escrow has worker_stake > 0.",
	}, s.handleFundEscrow)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "deposit_stake",
		Description: "Deposit the worker anti-Sybil stake into an escrow. Required before submit_work when worker_stake > 0 (check stake_required in get_escrow response). Handles both ETH and ERC20 stakes automatically.",
	}, s.handleDepositStake)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "submit_work",
		Description: "Submit work as the worker. Provide a URI pointing to the deliverable. For multi-milestone escrows, milestone_index is required (0-based). NOTE: The server must be running with the worker's private key (PRIVATE_KEY must match the escrow's worker address).",
	}, s.handleSubmitWork)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "verify_and_approve",
		Description: "Verify a submitted ZK proof on-chain and approve in a single transaction (verifier role). For multi-milestone escrows, milestone_index is required (0-based).",
	}, s.handleVerifyAndApprove)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "approve_work",
		Description: "Approve submitted work as buyer or verifier. For multi-milestone escrows, milestone_index is required (0-based). Both buyer and verifier must approve for dual-approval escrows.",
	}, s.handleApproveWork)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "dispute_work",
		Description: "Dispute or reject submitted work. role='buyer': open a dispute on submitted work. role='verifier': reject the submission. role='worker': escalate buyer/verifier silence to the arbitrator — use this when the buyer or verifier has not responded within the review period (calls escalateSilence on-chain). For multi-milestone escrows, milestone_index is required.",
	}, s.handleDisputeWork)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "resolve_dispute",
		Description: "Resolve a dispute as the arbitrator. worker_award_bps is 0–10000 (basis points): 10000 = full payment to worker, 0 = full refund to buyer.",
	}, s.handleResolveDispute)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_escrow",
		Description: "Get escrow details including current status, participants, amounts, and deadlines. Accepts either a numeric database ID or an on-chain escrow address (0x...). Returns stake_required=true when worker_stake > 0 and the escrow is funded but stake not yet deposited — the worker must call deposit_stake before submit_work. Note: status updates are eventually consistent (~15s indexer lag after on-chain transactions).",
	}, s.handleGetEscrow)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_escrows",
		Description: "List escrows, optionally filtered by role (buyer/worker/verifier/arbitrator), address, and status.",
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
		Name:        "mint_dct",
		Description: "Mint a Delegation Capability Token (DCT) scoped to escrow+subject+operations/resources+expiry.",
	}, s.handleMintDCT)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delegate_dct",
		Description: "Delegate a DCT with strict attenuation (subset operations/resources and no later expiry than parent).",
	}, s.handleDelegateDCT)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "introspect_dct",
		Description: "Introspect a DCT token and return active/revoked/expired state.",
	}, s.handleIntrospectDCT)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "revoke_dct",
		Description: "Revoke a DCT by token_id.",
	}, s.handleRevokeDCT)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_dct_audit",
		Description: "List DCT authorization audit log entries.",
	}, s.handleListDCTAudit)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_rfq",
		Description: "Broadcast a Task_RFQ (Request for Quote) describing a task for agents to bid on. Supports sealed bidding with commit/reveal windows. 'deadline' is task submission deadline; 'commit_deadline' and 'reveal_deadline' define bid privacy windows. Optional 'required_credentials_json' lets buyers require signed capability attestations from bidders. Paper §6.1: Task_RFQ broadcast; §4.6 Table 3: Web of Trust.",
	}, s.handleCreateRFQ)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "commit_bid",
		Description: "Submit a sealed-bid commitment during the commit phase.",
	}, s.handleCommitBid)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "reveal_bid",
		Description: "Reveal sealed-bid details during the reveal phase. The reveal must match the prior commitment. Include 'credentials_json' with signed attestation-v1 payloads to satisfy RFQ credential requirements.",
	}, s.handleRevealBid)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_bids",
		Description: "List revealed bids for an RFQ (buyer view) or by bidder address (worker view). Each bid includes expired and credential_verified fields.",
	}, s.handleListBids)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "accept_bid",
		Description: "Accept a bid on an RFQ. WARNING: This immediately deploys a new escrow contract on-chain (irreversible, costs gas). Returns escrow_id — the next step is to call fund_escrow. When the RFQ has required_credentials, only bids with credential_verified=true can be accepted. Paper §6.1: bid acceptance formalizes into escrow.",
	}, s.handleAcceptBid)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_attestation_chain",
		Description: "Get attestation chain(s) for an escrow, including signed completion-attestation-v1 links and verification status. Paper §4.8: recursive delegation verification and chain of custody.",
	}, s.handleGetAttestationChain)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "commit_checkpoint",
		Description: "Commit a checkpoint artifact as the active worker. Stores a state snapshot URI for mid-task resume by a replacement worker. Paper §6.1: checkpoint artifacts + partial compensation clauses.",
	}, s.handleCommitCheckpoint)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_checkpoints",
		Description: "List checkpoint artifacts for an escrow, newest first. Optionally filter by milestone_index. Used by replacement workers to discover resume state.",
	}, s.handleListCheckpoints)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_latest_checkpoint",
		Description: "Get the most recent checkpoint artifact for an escrow. Optionally scoped to a specific milestone. The primary entry point for a replacement worker to resume work.",
	}, s.handleGetLatestCheckpoint)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fund_via_mandate",
		Description: "Fund an escrow via an AP2 mandate with EIP-3009 gasless authorization. Paper §6: AP2 stake-on-bid + conditional settlement.",
	}, s.handleFundViaMandate)

	if s.cfg.EmergencyEnabled {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "emergency_override_dct",
			Description: "Emergency override for DCT operations (factory owner only). Bypasses normal authorization.",
		}, s.handleEmergencyOverrideDCT)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "freeze_address",
			Description: "Freeze an address so it cannot participate in new escrows. Paper §4.9: credential revocation propagation. Owner-only.",
		}, s.handleFreezeAddress)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "unfreeze_address",
			Description: "Unfreeze a previously frozen address, restoring its ability to participate in escrows. Owner-only.",
		}, s.handleUnfreezeAddress)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "freeze_escrow",
			Description: "Freeze an active escrow, blocking all participant actions. Paper §4.9: contract freeze with fund recovery. Owner-only.",
		}, s.handleFreezeEscrow)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "unfreeze_escrow",
			Description: "Unfreeze a frozen escrow, restoring normal operations. Owner-only.",
		}, s.handleUnfreezeEscrow)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "emergency_resolve",
			Description: "Force-settle a frozen escrow with a specified worker award. Paper §4.9: emergency resolution. Owner-only.",
		}, s.handleEmergencyResolve)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "list_frozen_addresses",
			Description: "List all currently frozen addresses. Paper §4.9: security incident monitoring.",
		}, s.handleListFrozenAddresses)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "list_emergency_actions",
			Description: "List emergency action audit log. Paper §4.9: security incident broadcasting.",
		}, s.handleListEmergencyActions)
	}

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

// resolveEscrowID accepts either a numeric DB ID ("3") or an on-chain address ("0x...")
// and returns the storage.Escrow. This eliminates the friction of callers needing to
// know which identifier type to use.
func (s *Server) resolveEscrowID(ctx context.Context, raw string) (*storage.Escrow, error) {
	if common.IsHexAddress(raw) {
		return s.db.GetEscrowByAddress(ctx, common.HexToAddress(raw).Hex())
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("escrow_id must be a numeric database ID or an on-chain address (0x...); got %q", raw)
	}
	return s.db.GetEscrow(ctx, id)
}

// persistAttestationChainTx creates an AttestationChain record and its links inside an
// existing transaction. The caller is responsible for Commit/Rollback.
func (s *Server) persistAttestationChainTx(ctx context.Context, tx *sql.Tx, escrowID int64, milestoneIdxPtr *int, chainResult attestation.ChainValidationResult, atts []attestation.CompletionAttestation) (*mcp.CallToolResult, error) {
	acRecord, acErr := s.db.CreateAttestationChainTx(ctx, tx, &storage.AttestationChain{
		EscrowID:                escrowID,
		MilestoneIndex:          milestoneIdxPtr,
		RootHash:                chainResult.RootHash,
		Verified:                chainResult.Valid,
		VerificationSummaryJSON: attestation.MarshalChainValidationResult(chainResult),
	})
	if acErr != nil {
		return textResult(fmt.Sprintf("failed to persist attestation chain: %v", acErr)), acErr
	}
	for _, att := range atts {
		if _, linkErr := s.db.CreateAttestationLinkTx(ctx, tx, &storage.AttestationLink{
			ChainID:       acRecord.ID,
			LinkID:        att.LinkID,
			ParentLinkID:  att.ParentLinkID,
			FromAddress:   att.FromAddress,
			ToAddress:     att.ToAddress,
			ChildEscrowID: att.ChildEscrowID,
			TaskSpecHash:  att.TaskSpecHash,
			OutcomeHash:   att.OutcomeHash,
			IssuedAt:      att.IssuedAt,
			ExpiresAt:     att.ExpiresAt,
			Nonce:         att.Nonce,
			Signature:     att.Signature,
		}); linkErr != nil {
			return textResult(fmt.Sprintf("failed to persist attestation link %s: %v", att.LinkID, linkErr)), linkErr
		}
	}
	return nil, nil
}

func (s *Server) handleCreateEscrow(ctx context.Context, req *mcp.CallToolRequest, args createEscrowArgs) (*mcp.CallToolResult, any, error) {
	amount, ok := new(big.Int).SetString(args.Amount.String(), 10)
	if !ok {
		return textResult("invalid amount"), nil, nil
	}
	if err := chain.ValidateComplexityFloor(amount, s.cfg.ComplexityFloor); err != nil {
		return textResult(err.Error()), nil, nil
	}
	deadline, err := strconv.ParseUint(args.SubmissionDeadline.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid submission_deadline: %v", err)), nil, nil
	}
	review, err := strconv.ParseUint(args.ReviewPeriodSeconds.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid review_period_seconds: %v", err)), nil, nil
	}
	dispute, err := strconv.ParseUint(args.DisputePeriodSeconds.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid dispute_period_seconds: %v", err)), nil, nil
	}
	arbTimeout, err := strconv.ParseUint(args.ArbitratorTimeoutSeconds.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid arbitrator_timeout_seconds: %v", err)), nil, nil
	}

	workerStakeVal := big.NewInt(0)
	if args.WorkerStake.String() != "" {
		ws, ok := new(big.Int).SetString(args.WorkerStake.String(), 10)
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
		msAmount, ok := new(big.Int).SetString(m.Amount.String(), 10)
		if !ok {
			return textResult("invalid milestone amount: " + m.Amount.String()), nil, nil
		}
		msDeadline, err := strconv.ParseUint(m.SubmissionDeadline.String(), 10, 64)
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
	if args.BackupDeadlineExtension.String() != "" {
		bde, err := strconv.ParseUint(args.BackupDeadlineExtension.String(), 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid backup_deadline_extension: %v", err)), nil, nil
		}
		backupDeadlineExt = bde
	}
	if backupDeadlineExt > 0 && (args.BackupWorker == "" || common.HexToAddress(args.BackupWorker) == (common.Address{})) {
		return textResult("backup_deadline_extension set but no backup_worker provided"), nil, nil
	}

	var zkVerifier common.Address
	if args.ZKVerifier != "" {
		if !common.IsHexAddress(args.ZKVerifier) {
			return textResult("invalid zk_verifier address"), nil, nil
		}
		zkVerifier = common.HexToAddress(args.ZKVerifier)
	}
	circuitID, err := parseProofHashHex(args.CircuitID)
	if err != nil {
		return textResult(fmt.Sprintf("invalid circuit_id: %v", err)), nil, nil
	}
	if (zkVerifier == common.Address{}) != (args.CircuitID == "") {
		return textResult("zk_verifier and circuit_id must either both be set or both omitted"), nil, nil
	}

	var serviceTier uint8
	if s := args.ServiceTier.String(); s != "" {
		if s != "0" && s != "1" {
			return textResult("invalid service_tier: must be 0 (low_assurance) or 1 (high_assurance)"), nil, nil
		}
		if s == "1" {
			serviceTier = 1
		}
	}

	// Validate all uint64→int64 conversions before any on-chain or DB side effects.
	submissionDeadline, err := numconv.Uint64ToInt64(deadline, "submission_deadline")
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	reviewPeriod, err := numconv.Uint64ToInt64(review, "review_period_seconds")
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	disputePeriod, err := numconv.Uint64ToInt64(dispute, "dispute_period_seconds")
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	arbitratorTimeout, err := numconv.Uint64ToInt64(arbTimeout, "arbitrator_timeout_seconds")
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	backupDeadline, err := numconv.Uint64ToInt64(backupDeadlineExt, "backup_deadline_extension")
	if err != nil {
		return textResult(err.Error()), nil, nil
	}

	msDeadlinesInt64 := make([]int64, len(milestones))
	for i, m := range milestones {
		msDeadlinesInt64[i], err = numconv.Uint64ToInt64(m.SubmissionDeadline, fmt.Sprintf("milestones[%d].submission_deadline", i))
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
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
		ServiceTier:              serviceTier,
		Milestones:               milestones,
		BackupWorker:             backupWorkerAddr,
		BackupDeadlineExtension:  backupDeadlineExt,
		ZKVerifier:               zkVerifier,
		CircuitID:                circuitID,
	}

	tx, err := s.chain.CreateEscrow(ctx, factory, params)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}

	result, err := chain.WaitMinedAndParseEscrow(ctx, s.chain, tx.Hash())
	if err != nil {
		return textResult(fmt.Sprintf("receipt error: %v", err)), nil, nil
	}

	task, err := s.db.CreateTask(ctx, args.Title, args.Description, specHash.Hex())
	if err != nil {
		return textResult(fmt.Sprintf("db error: %v", err)), nil, nil
	}

	milestoneCount := 1
	if len(milestones) > 0 {
		milestoneCount = len(milestones)
	}

	escrow, err := s.db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  s.cfg.ChainID,
		FactoryAddress:           s.cfg.FactoryAddress,
		EscrowAddress:            result.EscrowAddress.Hex(),
		EscrowID:                 result.EscrowID,
		Buyer:                    args.Buyer,
		Worker:                   args.Worker,
		Verifier:                 args.Verifier,
		Arbitrator:               args.Arbitrator,
		Amount:                   args.Amount.String(),
		WorkerStake:              workerStakeVal.String(),
		Token:                    tokenAddr.Hex(),
		Status:                   "created",
		SubmissionDeadline:       submissionDeadline,
		ReviewPeriodSeconds:      reviewPeriod,
		DisputePeriodSeconds:     disputePeriod,
		ArbitratorTimeoutSeconds: arbitratorTimeout,
		MilestoneCount:           milestoneCount,
		CurrentMilestone:         0,
		BackupWorker:             backupWorkerAddr.Hex(),
		BackupDeadlineExtension:  backupDeadline,
		ActiveWorker:             args.Worker,
		ServiceTier:              int(serviceTier),
		ZKVerifier:               zkVerifier.Hex(),
		CircuitID:                fmt.Sprintf("0x%x", circuitID),
	})
	if err != nil {
		return textResult(fmt.Sprintf("db error: %v", err)), nil, nil
	}

	for i, m := range milestones {
		_, err := s.db.CreateMilestone(ctx, &storage.MilestoneRecord{
			EscrowID:           escrow.ID,
			MilestoneIndex:     i,
			Amount:             m.Amount.String(),
			SubmissionDeadline: msDeadlinesInt64[i],
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
		"next_steps":      "Call fund_escrow with escrow_id to fund this escrow. After funding, the worker can submit_work.",
	})
}

func (s *Server) handleFundEscrow(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	amount, ok := new(big.Int).SetString(escrow.Amount, 10)
	if !ok {
		return textResult(fmt.Sprintf("malformed escrow amount in database: %q", escrow.Amount)), nil, nil
	}

	escrowAddr := common.HexToAddress(escrow.EscrowAddress)
	isERC20 := isERC20Token(escrow.Token)

	if isERC20 {
		tokenAddr := common.HexToAddress(escrow.Token)
		approveTx, err := s.chain.ApproveERC20(ctx, tokenAddr, escrowAddr, amount)
		if err != nil {
			return textResult(fmt.Sprintf("approve error: %v", chain.HumanizeError(err))), nil, nil
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
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		stakeHint := "The worker can now call submit_work."
		if hasStake(escrow) {
			stakeHint = "Worker must call deposit_stake before submit_work (stake_required=true)."
		}
		return jsonResult(map[string]any{
			"tx_hash":    tx.Hash().Hex(),
			"next_steps": "Status updates are eventually consistent (~15s indexer lag). Poll get_escrow until status=funded. " + stakeHint,
		})
	}

	tx, err := s.chain.Fund(ctx, escrowAddr, amount)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	stakeHint := "The worker can now call submit_work."
	if hasStake(escrow) {
		stakeHint = "Worker must call deposit_stake before submit_work (stake_required=true)."
	}
	return jsonResult(map[string]any{
		"tx_hash":    tx.Hash().Hex(),
		"next_steps": "Status updates are eventually consistent (~15s indexer lag). Poll get_escrow until status=funded. " + stakeHint,
	})
}

func (s *Server) handleDepositStake(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	stakeAmount, ok := new(big.Int).SetString(escrow.WorkerStake, 10)
	if !ok || stakeAmount.Sign() <= 0 {
		return textResult("this escrow does not require a worker stake"), nil, nil
	}

	escrowAddr := common.HexToAddress(escrow.EscrowAddress)

	if isERC20Token(escrow.Token) {
		tokenAddr := common.HexToAddress(escrow.Token)
		approveTx, err := s.chain.ApproveERC20(ctx, tokenAddr, escrowAddr, stakeAmount)
		if err != nil {
			return textResult(fmt.Sprintf("approve error: %v", chain.HumanizeError(err))), nil, nil
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
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Worker can now call submit_work."})
	}

	tx, err := s.chain.DepositStake(ctx, escrowAddr, stakeAmount)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Worker can now call submit_work."})
}

func (s *Server) handleSubmitWork(ctx context.Context, req *mcp.CallToolRequest, args submitArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	// Warn if the server's signing key doesn't match the worker address.
	signerAddr := strings.ToLower(s.chain.Address().Hex())
	workerAddr := strings.ToLower(escrow.ActiveWorker)
	if workerAddr == "" {
		workerAddr = strings.ToLower(escrow.Worker)
	}
	if signerAddr != workerAddr {
		return textResult(fmt.Sprintf(
			"submit_work requires the worker's signature. The server is configured with key for %s, but the escrow worker is %s. "+
				"Set PRIVATE_KEY in the environment or run a server instance with the worker's private key.",
			signerAddr, workerAddr,
		)), nil, nil
	}

	// Attestation chain validation for sub-delegation (paper §4.8).
	childEscrows, childErr := s.db.ListChildEscrows(ctx, escrow.ID)
	if childErr != nil {
		return textResult(fmt.Sprintf("failed to check child escrows: %v", childErr)), nil, nil
	}

	var milestoneIdxPtr *int
	if escrow.MilestoneCount > 1 && args.MilestoneIndex.String() == "" {
		return textResult("milestone_index required for multi-milestone escrow"), nil, nil
	}
	if args.MilestoneIndex.String() != "" {
		msVal, msErr := strconv.Atoi(args.MilestoneIndex.String())
		if msErr != nil {
			return textResult(fmt.Sprintf("invalid milestone_index: %v", msErr)), nil, nil
		}
		if msVal < 0 || msVal >= escrow.MilestoneCount {
			return textResult(fmt.Sprintf("invalid milestone_index: %d out of range [0, %d)", msVal, escrow.MilestoneCount)), nil, nil
		}
		milestoneIdxPtr = &msVal
	}

	if len(childEscrows) > 0 {
		atts, parseErr := attestation.ParseCompletionAttestations(args.AttestationChainJSON)
		if parseErr != nil {
			return textResult(fmt.Sprintf("invalid attestation_chain_json: %v", parseErr)), nil, nil
		}
		if len(atts) == 0 {
			return textResult("attestation_chain_json required when escrow has sub-delegated child escrows"), nil, nil
		}
		childIDs := make([]int64, len(childEscrows))
		for i, ce := range childEscrows {
			childIDs[i] = ce.ID
		}
		chainResult := attestation.ValidateChain(atts, childIDs, time.Now())
		if !chainResult.Valid {
			return textResult("attestation chain validation failed: " + strings.Join(chainResult.Reasons, "; ")), nil, nil
		}
		tx, txErr := s.db.BeginTx(ctx)
		if txErr != nil {
			return textResult(fmt.Sprintf("failed to begin attestation persistence transaction: %v", txErr)), nil, nil
		}
		defer tx.Rollback()

		if errResult, persistErr := s.persistAttestationChainTx(ctx, tx, escrow.ID, milestoneIdxPtr, chainResult, atts); persistErr != nil {
			return errResult, nil, nil
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return textResult(fmt.Sprintf("failed to persist attestation chain: %v", commitErr)), nil, nil
		}
	} else if args.AttestationChainJSON != "" && args.AttestationChainJSON != "[]" {
		atts, parseErr := attestation.ParseCompletionAttestations(args.AttestationChainJSON)
		if parseErr != nil {
			return textResult(fmt.Sprintf("invalid attestation_chain_json: %v", parseErr)), nil, nil
		}
		if len(atts) > 0 {
			chainResult := attestation.ValidateChain(atts, nil, time.Now())
			if !chainResult.Valid {
				return textResult("attestation chain validation failed: " + strings.Join(chainResult.Reasons, "; ")), nil, nil
			}
			tx, txErr := s.db.BeginTx(ctx)
			if txErr != nil {
				return textResult(fmt.Sprintf("failed to begin attestation persistence transaction: %v", txErr)), nil, nil
			}
			defer tx.Rollback()

			if errResult, persistErr := s.persistAttestationChainTx(ctx, tx, escrow.ID, milestoneIdxPtr, chainResult, atts); persistErr != nil {
				return errResult, nil, nil
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return textResult(fmt.Sprintf("failed to persist attestation chain: %v", commitErr)), nil, nil
			}
		}
	}

	hash := crypto.Keccak256Hash([]byte(args.SubmissionURI))
	var hashBytes [32]byte
	copy(hashBytes[:], hash.Bytes())
	proofHash, err := parseProofHashHex(args.ProofHash)
	if err != nil {
		return textResult(fmt.Sprintf("invalid proof_hash: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		msIdx, msConvErr := numconv.IntToUint8(*milestoneIdxPtr, "milestone_index")
		if msConvErr != nil {
			return textResult(msConvErr.Error()), nil, nil
		}
		tx, err := s.chain.SubmitMilestone(ctx, addr, msIdx, hashBytes, args.SubmissionURI, proofHash)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Buyer/verifier should call approve_work to approve the submission."})
	}

	tx, err := s.chain.Submit(ctx, addr, hashBytes, args.SubmissionURI, proofHash)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Buyer/verifier should call approve_work to approve the submission."})
}

func (s *Server) handleApproveWork(ctx context.Context, req *mcp.CallToolRequest, args approveArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex.String() == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex.String())
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		switch args.Role {
		case "buyer":
			tx, err := s.chain.ApproveMilestoneByBuyer(ctx, addr, msIdx)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Check get_reputation to see updated counters once the indexer settles (~15s)."})
		case "verifier":
			tx, err := s.chain.ApproveMilestoneByVerifier(ctx, addr, msIdx)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Check get_reputation to see updated counters once the indexer settles (~15s)."})
		default:
			return textResult("role must be 'buyer' or 'verifier'"), nil, nil
		}
	}

	switch args.Role {
	case "buyer":
		tx, err := s.chain.ApproveByBuyer(ctx, addr)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Check get_reputation to see updated counters once the indexer settles (~15s)."})
	case "verifier":
		tx, err := s.chain.ApproveByVerifier(ctx, addr)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex(), "next_steps": "Check get_reputation to see updated counters once the indexer settles (~15s)."})
	default:
		return textResult("role must be 'buyer' or 'verifier'"), nil, nil
	}
}

func (s *Server) handleVerifyAndApprove(ctx context.Context, req *mcp.CallToolRequest, args verifyAndApproveArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}
	proofBytes, err := parseProofHexBytes(args.Proof)
	if err != nil {
		return textResult(fmt.Sprintf("invalid proof: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)
	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex.String() == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex.String())
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		tx, err := s.chain.VerifyAndApproveMilestone(ctx, addr, msIdx, proofBytes)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{
			"tx_hash":    tx.Hash().Hex(),
			"next_steps": "Verification and approval submitted. Poll get_escrow for status updates (~15s indexer lag).",
		})
	}

	tx, err := s.chain.VerifyAndApprove(ctx, addr, proofBytes)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}
	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{
		"tx_hash":    tx.Hash().Hex(),
		"next_steps": "Verification and approval submitted. Poll get_escrow for status updates (~15s indexer lag).",
	})
}

func (s *Server) handleDisputeWork(ctx context.Context, req *mcp.CallToolRequest, args disputeArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex.String() == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex.String())
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		switch args.Role {
		case "buyer":
			tx, err := s.chain.DisputeMilestone(ctx, addr, msIdx, args.ReasonURI)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{
				"tx_hash":    tx.Hash().Hex(),
				"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
			})
		case "verifier":
			tx, err := s.chain.RejectMilestoneByVerifier(ctx, addr, msIdx, args.ReasonURI)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{
				"tx_hash":    tx.Hash().Hex(),
				"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
			})
		case "worker":
			tx, err := s.chain.EscalateMilestoneSilence(ctx, addr, msIdx, args.ReasonURI)
			if err != nil {
				return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
			}
			_ = s.idx.RunOnce(ctx)
			return jsonResult(map[string]any{
				"tx_hash":    tx.Hash().Hex(),
				"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
			})
		default:
			return textResult("role must be 'buyer', 'verifier', or 'worker'"), nil, nil
		}
	}

	switch args.Role {
	case "buyer":
		tx, err := s.chain.Dispute(ctx, addr, args.ReasonURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{
			"tx_hash":    tx.Hash().Hex(),
			"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
		})
	case "verifier":
		tx, err := s.chain.RejectByVerifier(ctx, addr, args.ReasonURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{
			"tx_hash":    tx.Hash().Hex(),
			"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
		})
	case "worker":
		tx, err := s.chain.EscalateSilence(ctx, addr, args.ReasonURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{
			"tx_hash":    tx.Hash().Hex(),
			"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
		})
	default:
		return textResult("role must be 'buyer', 'verifier', or 'worker'"), nil, nil
	}
}

func (s *Server) handleResolveDispute(ctx context.Context, req *mcp.CallToolRequest, args resolveArgs) (*mcp.CallToolResult, any, error) {
	bps, err := strconv.ParseUint(args.WorkerAwardBps.String(), 10, 16)
	if err != nil {
		return textResult(fmt.Sprintf("invalid worker_award_bps: %v", err)), nil, nil
	}
	if bps > 10_000 {
		return textResult("worker_award_bps must be between 0 and 10000"), nil, nil
	}

	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	addr := common.HexToAddress(escrow.EscrowAddress)

	if escrow.MilestoneCount > 1 {
		if args.MilestoneIndex.String() == "" {
			return textResult("milestone_index required for multi-milestone escrow"), nil, nil
		}
		msIdx, err := parseMilestoneIndex(args.MilestoneIndex.String())
		if err != nil {
			return textResult(err.Error()), nil, nil
		}
		tx, err := s.chain.ResolveMilestoneDispute(ctx, addr, msIdx, uint16(bps), args.ResolutionURI)
		if err != nil {
			return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
		}
		_ = s.idx.RunOnce(ctx)
		return jsonResult(map[string]any{
			"tx_hash":    tx.Hash().Hex(),
			"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
		})
	}

	tx, err := s.chain.ResolveDispute(ctx, addr, uint16(bps), args.ResolutionURI)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{
		"tx_hash":    tx.Hash().Hex(),
		"next_steps": "Check get_escrow for updated status after indexer settles (~15s).",
	})
}

func (s *Server) handleGetEscrow(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	stakeRequired := false
	if hasStake(escrow) && escrow.Status == "funded" {
		deposited, err := s.db.EventExistsForContract(ctx, escrow.EscrowAddress, "WorkerStakeDeposited")
		if err != nil {
			return textResult(fmt.Sprintf("failed to check stake status: %v", err)), nil, nil
		}
		stakeRequired = !deposited
	}

	result := map[string]any{
		"escrow":         escrow,
		"stake_required": stakeRequired,
	}

	if escrow.MilestoneCount > 1 {
		milestones, err := s.db.GetMilestonesByEscrow(ctx, escrow.ID)
		if err != nil {
			return textResult(fmt.Sprintf("failed to fetch milestones for escrow %d: %v", escrow.ID, err)), nil, nil
		}
		result["milestones"] = milestones
	}

	return jsonResult(result)
}

func (s *Server) handleListEscrows(ctx context.Context, req *mcp.CallToolRequest, args listArgs) (*mcp.CallToolResult, any, error) {
	escrows, err := s.db.ListEscrows(ctx, args.Role, args.Address, args.Status)
	if err != nil {
		return textResult(fmt.Sprintf("error: %v", err)), nil, nil
	}
	return jsonResult(escrows)
}

func (s *Server) handleAbortRemainingMilestones(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}
	if escrow.MilestoneCount <= 1 {
		return textResult("abort_remaining_milestones is only available for multi-milestone escrows"), nil, nil
	}

	tx, err := s.chain.AbortRemainingMilestones(ctx, common.HexToAddress(escrow.EscrowAddress))
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) handleActivateBackup(ctx context.Context, req *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
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
		return textResult(fmt.Sprintf("chain error: %v", chain.HumanizeError(err))), nil, nil
	}

	_ = s.idx.RunOnce(ctx)
	return jsonResult(map[string]any{"tx_hash": tx.Hash().Hex()})
}

func (s *Server) dctService() *dct.Service {
	return &dct.Service{
		DB:           s.db,
		Audit:        &authz.SQLiteAuditStore{DB: s.db.SQLDB()},
		FactoryOwner: s.cfg.OwnerAddress,
	}
}

// withCallerCtx attaches an authenticated caller principal to the context.
// If callerAddr is empty, an unauthenticated principal is set.
func withCallerCtx(ctx context.Context, callerAddr string) context.Context {
	callerAddr = strings.TrimSpace(callerAddr)
	p := authz.Principal{
		Address:       strings.ToLower(callerAddr),
		Authenticated: callerAddr != "",
	}
	return authz.WithCaller(ctx, p)
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
		rep, err := s.db.GetReputation(ctx, addr, args.Role)
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

	reps, err := s.db.GetReputationByAddress(ctx, addr)
	if err != nil {
		return nil, nil, fmt.Errorf("get reputation by address: %w", err)
	}
	if len(reps) == 0 {
		return jsonResult(map[string]any{
			"address": addr,
			"roles":   []any{},
			"note":    "No on-chain reputation recorded yet. Reputation counters update after the indexer processes OutcomeRecorded events (~15s after settlement).",
		})
	}
	return jsonResult(map[string]any{
		"address": addr,
		"roles":   reps,
	})
}

func (s *Server) handleMintDCT(ctx context.Context, _ *mcp.CallToolRequest, args mintDCTArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	exp, err := strconv.ParseInt(args.ExpiresAt.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid expires_at: %v", err)), nil, nil
	}
	ctx = withCallerCtx(ctx, args.Caller)
	rec, token, err := s.dctService().Mint(ctx, dct.MintParams{EscrowID: escrowID, Subject: args.Subject, Issuer: args.Issuer, Operations: args.Operations, Resources: args.Resources, ExpiresAt: exp})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	return jsonResult(map[string]any{"token": token, "record": rec})
}

func (s *Server) handleDelegateDCT(ctx context.Context, _ *mcp.CallToolRequest, args delegateDCTArgs) (*mcp.CallToolResult, any, error) {
	exp, err := strconv.ParseInt(args.ExpiresAt.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid expires_at: %v", err)), nil, nil
	}
	ctx = withCallerCtx(ctx, args.Caller)
	rec, token, err := s.dctService().Delegate(ctx, dct.DelegateParams{ParentToken: args.ParentToken, Subject: args.Subject, Issuer: args.Issuer, Operations: args.Operations, Resources: args.Resources, ExpiresAt: exp})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	return jsonResult(map[string]any{"token": token, "record": rec})
}

func (s *Server) handleIntrospectDCT(ctx context.Context, _ *mcp.CallToolRequest, args introspectDCTArgs) (*mcp.CallToolResult, any, error) {
	rec, active, reasons, err := s.dctService().Introspect(ctx, args.Token)
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	return jsonResult(map[string]any{"token": rec, "active": active, "reasons": reasons})
}

func (s *Server) handleRevokeDCT(ctx context.Context, _ *mcp.CallToolRequest, args revokeDCTArgs) (*mcp.CallToolResult, any, error) {
	ctx = withCallerCtx(ctx, args.Caller)
	if err := s.dctService().Revoke(ctx, dct.RevokeParams{TokenID: args.TokenID, Reason: args.Reason, By: args.By}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return textResult("token not found"), nil, nil
		}
		return textResult(err.Error()), nil, nil
	}
	return jsonResult(map[string]any{"status": "revoked", "token_id": args.TokenID})
}

func (s *Server) handleEmergencyOverrideDCT(ctx context.Context, _ *mcp.CallToolRequest, args emergencyOverrideDCTArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	ctx = withCallerCtx(ctx, args.Owner)
	if err := s.dctService().EmergencyOverride(ctx, dct.EmergencyOverrideParams{
		EscrowID:      escrowID,
		Operation:     args.Operation,
		CallerAddress: args.CallerAddress,
		Reason:        args.Reason,
		OwnerAddress:  args.Owner,
	}); err != nil {
		switch {
		case errors.Is(err, dct.ErrUnauthorized),
			errors.Is(err, sql.ErrNoRows),
			strings.Contains(err.Error(), "owner address is required"),
			strings.Contains(err.Error(), "override reason is required"),
			strings.Contains(err.Error(), "unsupported override operation"):
			return textResult(err.Error()), nil, nil
		default:
			return textResult("internal error"), nil, nil
		}
	}
	return jsonResult(map[string]any{"status": "override_applied", "escrow_id": escrowID, "operation": args.Operation})
}

func (s *Server) handleListDCTAudit(ctx context.Context, _ *mcp.CallToolRequest, args listDCTAuditArgs) (*mcp.CallToolResult, any, error) {
	var escrowID int64
	if args.EscrowID.String() != "" {
		var err error
		escrowID, err = strconv.ParseInt(args.EscrowID.String(), 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
		}
	}
	limit := 50
	if args.Limit.String() != "" {
		if v, err := strconv.Atoi(args.Limit.String()); err == nil && v > 0 {
			limit = v
		}
	}
	offset := 0
	if args.Offset.String() != "" {
		if v, err := strconv.Atoi(args.Offset.String()); err == nil && v >= 0 {
			offset = v
		}
	}
	records, err := s.dctService().Audit.ListAuthzAudit(ctx, escrowID, limit, offset)
	if err != nil {
		return textResult(fmt.Sprintf("audit query error: %v", err)), nil, nil
	}
	return jsonResult(map[string]any{"audit_entries": records, "count": len(records)})
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
	deadline, err := strconv.ParseInt(args.Deadline.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid deadline: %v", err)), nil, nil
	}
	review, err := strconv.ParseInt(args.ReviewPeriodSeconds.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid review_period_seconds: %v", err)), nil, nil
	}
	dispute, err := strconv.ParseInt(args.DisputePeriodSeconds.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid dispute_period_seconds: %v", err)), nil, nil
	}
	arbTimeout, err := strconv.ParseInt(args.ArbitratorTimeoutSeconds.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid arbitrator_timeout_seconds: %v", err)), nil, nil
	}
	expiresAt, err := strconv.ParseInt(args.ExpiresAt.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid expires_at: %v", err)), nil, nil
	}
	var commitDeadline int64
	if args.CommitDeadline.String() != "" {
		commitDeadline, err = strconv.ParseInt(args.CommitDeadline.String(), 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid commit_deadline: %v", err)), nil, nil
		}
	}
	var revealDeadline int64
	if args.RevealDeadline.String() != "" {
		revealDeadline, err = strconv.ParseInt(args.RevealDeadline.String(), 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid reveal_deadline: %v", err)), nil, nil
		}
	}

	// Normalize token field.
	token := normalizeToken(args.Token)

	var serviceTier int
	if s := args.ServiceTier.String(); s != "" {
		if s != "0" && s != "1" {
			return textResult("invalid service_tier: must be 0 (low_assurance) or 1 (high_assurance)"), nil, nil
		}
		if s == "1" {
			serviceTier = 1
		}
	}

	var parentEscrowID *int64
	if args.ParentEscrowID.String() != "" {
		pid, pidErr := strconv.ParseInt(args.ParentEscrowID.String(), 10, 64)
		if pidErr != nil {
			return textResult(fmt.Sprintf("invalid parent_escrow_id: %v", pidErr)), nil, nil
		}
		parentEscrowID = &pid
	}

	svc := s.biddingService()
	rfq, err := svc.CreateRFQ(ctx, bidding.CreateRFQParams{
		Title:                    args.Title,
		Description:              args.Description,
		Buyer:                    args.Buyer,
		Token:                    token,
		BudgetMin:                args.BudgetMin.String(),
		BudgetMax:                args.BudgetMax.String(),
		Deadline:                 deadline,
		ReviewPeriodSeconds:      review,
		DisputePeriodSeconds:     dispute,
		ArbitratorTimeoutSeconds: arbTimeout,
		Verifier:                 args.Verifier,
		Arbitrator:               args.Arbitrator,
		WorkerStake:              args.WorkerStake.String(),
		MilestonesJSON:           args.MilestonesJSON,
		RequirementsJSON:         args.RequirementsJSON,
		RequiredProofProtocol:    args.RequiredProofProtocol,
		RequiredCredentialsJSON:  args.RequiredCredentialsJSON,
		CommitDeadline:           commitDeadline,
		RevealDeadline:           revealDeadline,
		ServiceTier:              serviceTier,
		ExpiresAt:                expiresAt,
		ParentEscrowID:           parentEscrowID,
	})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}

	return jsonResult(map[string]any{
		"rfq_id":     rfq.ID,
		"status":     rfq.Status,
		"next_steps": "Workers should call commit_bid during commit phase, then reveal_bid during reveal phase. Buyer can call accept_bid after reveal phase ends.",
	})
}

func (s *Server) handleCommitBid(ctx context.Context, req *mcp.CallToolRequest, args commitBidArgs) (*mcp.CallToolResult, any, error) {
	rfqID, err := strconv.ParseInt(args.RFQID.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid rfq_id: %v", err)), nil, nil
	}

	svc := s.biddingService()
	commit, err := svc.CommitBid(ctx, bidding.CommitBidParams{
		RFQID:      rfqID,
		Bidder:     args.Bidder,
		Commitment: args.Commitment,
		Nonce:      args.Nonce,
	})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}

	return jsonResult(map[string]any{
		"commit_id":  commit.ID,
		"status":     commit.Status,
		"next_steps": "Reveal this bid in reveal phase with reveal_bid using the same bidder and nonce.",
	})
}

func (s *Server) handleRevealBid(ctx context.Context, req *mcp.CallToolRequest, args revealBidArgs) (*mcp.CallToolResult, any, error) {
	rfqID, err := strconv.ParseInt(args.RFQID.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid rfq_id: %v", err)), nil, nil
	}
	var estimatedDuration int64
	if args.EstimatedDuration.String() != "" {
		estimatedDuration, err = strconv.ParseInt(args.EstimatedDuration.String(), 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid estimated_duration: %v", err)), nil, nil
		}
	}
	var expiresAt int64
	if args.ExpiresAt.String() != "" {
		expiresAt, err = strconv.ParseInt(args.ExpiresAt.String(), 10, 64)
		if err != nil {
			return textResult(fmt.Sprintf("invalid expires_at: %v", err)), nil, nil
		}
	}

	svc := s.biddingService()
	bid, err := svc.RevealBid(ctx, bidding.RevealBidParams{
		RFQID:             rfqID,
		Bidder:            args.Bidder,
		Nonce:             args.Nonce,
		Salt:              args.Salt,
		Amount:            args.Amount.String(),
		EstimatedDuration: estimatedDuration,
		ReputationBond:    args.ReputationBond.String(),
		MilestonesJSON:    args.MilestonesJSON,
		Message:           args.Message,
		ExpiresAt:         expiresAt,
		StakeMandateID:    args.StakeMandateID,
		CredentialsJSON:   args.CredentialsJSON,
	})
	if err != nil {
		return textResult(err.Error()), nil, nil
	}

	resp := map[string]any{
		"bid_id":              bid.ID,
		"status":              bid.Status,
		"credential_verified": bid.CredentialVerified,
		"next_steps":          "Buyer can accept this bid after reveal phase ends using accept_bid.",
	}
	if summary := bid.CredentialMatchSummary; summary != "" && summary != "{}" {
		if json.Valid([]byte(summary)) {
			resp["credential_match"] = json.RawMessage(summary)
		}
	}
	return jsonResult(resp)
}

func (s *Server) handleListBids(ctx context.Context, req *mcp.CallToolRequest, args listBidsArgs) (*mcp.CallToolResult, any, error) {
	var bids []*storage.Bid
	var err error

	switch {
	case args.RFQID.String() != "":
		rfqID, parseErr := strconv.ParseInt(args.RFQID.String(), 10, 64)
		if parseErr != nil {
			return textResult(fmt.Sprintf("invalid rfq_id: %v", parseErr)), nil, nil
		}
		rfq, getErr := s.db.GetRFQ(ctx, rfqID)
		if getErr == nil {
			if time.Now().Unix() > rfq.RevealDeadline {
				if expireErr := s.db.ExpireCommittedBidCommits(ctx, rfqID); expireErr != nil {
					return textResult(fmt.Sprintf("error: %v", expireErr)), nil, nil
				}
			}
		} else if !errors.Is(getErr, sql.ErrNoRows) {
			return textResult(fmt.Sprintf("error: %v", getErr)), nil, nil
		}
		bids, err = s.db.ListBidsByRFQ(ctx, rfqID)
	case args.Bidder != "":
		bids, err = s.db.ListBidsByBidder(ctx, args.Bidder)
	default:
		return textResult("provide either rfq_id or bidder"), nil, nil
	}
	if err != nil {
		return textResult(fmt.Sprintf("error: %v", err)), nil, nil
	}

	now := time.Now().Unix()
	type bidWithExpiry struct {
		*storage.Bid
		Expired bool `json:"expired"`
	}
	out := make([]bidWithExpiry, len(bids))
	for i, b := range bids {
		out[i] = bidWithExpiry{Bid: b, Expired: b.ExpiresAt <= now}
	}
	return jsonResult(out)
}

func (s *Server) handleAcceptBid(ctx context.Context, req *mcp.CallToolRequest, args acceptBidArgs) (*mcp.CallToolResult, any, error) {
	rfqID, err := strconv.ParseInt(args.RFQID.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid rfq_id: %v", err)), nil, nil
	}
	bidID, err := strconv.ParseInt(args.BidID.String(), 10, 64)
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
		return textResult(fmt.Sprintf("accept_bid error: %v", chain.HumanizeError(err))), nil, nil
	}

	return jsonResult(map[string]any{
		"escrow_id":       result.Escrow.ID,
		"task_id":         result.Task.ID,
		"tx_hash":         result.TxHash,
		"escrow_address":  result.Escrow.EscrowAddress,
		"chain_escrow_id": result.Escrow.EscrowID,
		"bid_id":          result.Bid.ID,
		"bid_status":      result.Bid.Status,
		"next_steps":      "An escrow has been deployed on-chain. Call fund_escrow with the escrow_id to fund it.",
	})
}

func (s *Server) handleGetAttestationChain(ctx context.Context, req *mcp.CallToolRequest, args getAttestationChainArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return textResult(fmt.Sprintf("escrow not found: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("failed to look up escrow: %v", err)), nil, nil
	}

	chains, err := s.db.GetAttestationChainsByEscrow(ctx, escrow.ID)
	if err != nil {
		return textResult(fmt.Sprintf("failed to get attestation chains: %v", err)), nil, nil
	}

	type chainWithLinks struct {
		Chain *storage.AttestationChain  `json:"chain"`
		Links []*storage.AttestationLink `json:"links"`
	}
	result := make([]chainWithLinks, 0, len(chains))
	for _, ch := range chains {
		links, linkErr := s.db.GetAttestationLinksByChain(ctx, ch.ID)
		if linkErr != nil {
			return textResult(fmt.Sprintf("failed to get links for chain %d: %v", ch.ID, linkErr)), nil, nil
		}
		result = append(result, chainWithLinks{Chain: ch, Links: links})
	}

	return jsonResult(map[string]any{
		"escrow_id": escrow.ID,
		"chains":    result,
	})
}

func (s *Server) handleCommitCheckpoint(ctx context.Context, req *mcp.CallToolRequest, args commitCheckpointArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return textResult(fmt.Sprintf("escrow not found: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("failed to look up escrow: %v", err)), nil, nil
	}

	if args.StateSnapshotURI == "" {
		return textResult("state_snapshot_uri is required"), nil, nil
	}
	if args.CommittedBy == "" {
		return textResult("committed_by is required"), nil, nil
	}
	if !strings.EqualFold(args.CommittedBy, escrow.ActiveWorker) {
		return textResult("only the active worker can commit checkpoints"), nil, nil
	}

	milestoneIndex, milestoneErr := parseOptionalMilestoneIndex(args.MilestoneIndex.String(), escrow.MilestoneCount)
	if milestoneErr != nil {
		return textResult(milestoneErr.Error()), nil, nil
	}

	var completionPct *int
	if cp := args.CompletionPct.String(); cp != "" {
		v, parseErr := strconv.Atoi(cp)
		if parseErr != nil {
			return textResult(fmt.Sprintf("invalid completion_pct: %v", parseErr)), nil, nil
		}
		if v < 0 || v > 100 {
			return textResult("completion_pct must be 0-100"), nil, nil
		}
		completionPct = &v
	}

	schemaVersion := args.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = "checkpoint-v1"
	}
	if args.MetadataJSON != "" && !json.Valid([]byte(args.MetadataJSON)) {
		return textResult("metadata_json must be valid JSON"), nil, nil
	}

	cp, err := s.db.CreateCheckpoint(ctx, &storage.Checkpoint{
		EscrowID:         escrow.ID,
		MilestoneIndex:   milestoneIndex,
		StateSnapshotURI: args.StateSnapshotURI,
		SnapshotHash:     args.SnapshotHash,
		SchemaVersion:    schemaVersion,
		CommittedBy:      args.CommittedBy,
		CompletionPct:    completionPct,
		MetadataJSON:     args.MetadataJSON,
	})
	if err != nil {
		return textResult(fmt.Sprintf("failed to create checkpoint: %v", err)), nil, nil
	}

	if s.bus != nil {
		s.bus.Publish(events.Event{
			Name:      events.EventCheckpointCommitted,
			Escrow:    escrow.EscrowAddress,
			Level:     events.L1,
			Timestamp: time.Now(),
			ID:        fmt.Sprintf("checkpoint-%d", cp.ID),
			Payload: map[string]any{
				"checkpoint_id":      cp.ID,
				"escrow_id":          escrow.ID,
				"state_snapshot_uri": cp.StateSnapshotURI,
				"committed_by":       cp.CommittedBy,
			},
		})
	}

	return jsonResult(map[string]any{
		"checkpoint": cp,
		"next_steps": "Checkpoint committed. A replacement worker can retrieve it via get_latest_checkpoint to resume work.",
	})
}

func (s *Server) handleListCheckpoints(ctx context.Context, req *mcp.CallToolRequest, args listCheckpointsArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return textResult(fmt.Sprintf("escrow not found: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("failed to look up escrow: %v", err)), nil, nil
	}

	milestoneIndex, milestoneErr := parseOptionalMilestoneIndex(args.MilestoneIndex.String(), escrow.MilestoneCount)
	if milestoneErr != nil {
		return textResult(milestoneErr.Error()), nil, nil
	}

	checkpoints, err := s.db.ListCheckpointsByEscrow(ctx, escrow.ID, milestoneIndex)
	if err != nil {
		return textResult(fmt.Sprintf("failed to list checkpoints: %v", err)), nil, nil
	}

	return jsonResult(map[string]any{
		"escrow_id":   escrow.ID,
		"checkpoints": checkpoints,
		"count":       len(checkpoints),
	})
}

func (s *Server) handleGetLatestCheckpoint(ctx context.Context, req *mcp.CallToolRequest, args getLatestCheckpointArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return textResult(fmt.Sprintf("escrow not found: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("failed to look up escrow: %v", err)), nil, nil
	}

	milestoneIndex, milestoneErr := parseOptionalMilestoneIndex(args.MilestoneIndex.String(), escrow.MilestoneCount)
	if milestoneErr != nil {
		return textResult(milestoneErr.Error()), nil, nil
	}

	cp, err := s.db.GetLatestCheckpoint(ctx, escrow.ID, milestoneIndex)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return textResult("no checkpoints found for this escrow"), nil, nil
		}
		return textResult(fmt.Sprintf("failed to get latest checkpoint: %v", err)), nil, nil
	}

	return jsonResult(map[string]any{
		"checkpoint": cp,
		"next_steps": "Use the state_snapshot_uri to retrieve the checkpoint artifact and resume work from this state.",
	})
}

func (s *Server) handleFundViaMandate(ctx context.Context, req *mcp.CallToolRequest, args fundViaMandateArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}
	escrowID := escrow.ID

	v, err := strconv.ParseUint(args.AuthV.String(), 10, 8)
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
	if args.Limit.String() != "" {
		v, err := strconv.Atoi(args.Limit.String())
		if err != nil || v <= 0 {
			return textResult("invalid limit: must be a positive integer"), nil, nil //nolint:nilerr // err is from Atoi; we return a user-facing message, not the parse error
		}
		if v > 200 {
			v = 200
		}
		limit = v
	}

	recent := s.bus.RecentEvents(args.EscrowAddress, granularity, args.SinceID, limit)

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

// --- Emergency response protocol handlers (paper §4.9) ---

func (s *Server) handleFreezeAddress(ctx context.Context, _ *mcp.CallToolRequest, args addressArgs) (*mcp.CallToolResult, any, error) {
	if !common.IsHexAddress(args.Address) {
		return textResult("invalid address"), nil, nil
	}
	addr := common.HexToAddress(args.Address)
	factory := s.idx.FactoryAddress()

	tx, err := s.chain.FreezeAddress(ctx, factory, addr)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}
	receipt, err := chain.WaitMined(ctx, s.chain, tx.Hash())
	if err != nil {
		return textResult(fmt.Sprintf("tx submitted (%s) but receipt unavailable: %v", tx.Hash().Hex(), err)), nil, nil
	}
	if receipt.Status != 1 {
		return textResult("transaction reverted: " + tx.Hash().Hex()), nil, nil
	}

	txHash := receipt.TxHash.Hex()
	if err := s.db.UpsertFrozenAddress(ctx, strings.ToLower(addr.Hex()), "", "mcp"); err != nil {
		return textResult(fmt.Sprintf("db error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.db.CreateEmergencyAction(ctx, "freeze_address", strings.ToLower(addr.Hex()), "", "", txHash); err != nil {
		return textResult(fmt.Sprintf("db audit error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.idx.RunOnce(ctx); err != nil {
		return textResult(fmt.Sprintf("indexer sync error after successful tx %s: %v", txHash, err)), nil, nil
	}
	return jsonResult(map[string]any{"tx_hash": txHash, "address": addr.Hex()})
}

func (s *Server) handleUnfreezeAddress(ctx context.Context, _ *mcp.CallToolRequest, args addressArgs) (*mcp.CallToolResult, any, error) {
	if !common.IsHexAddress(args.Address) {
		return textResult("invalid address"), nil, nil
	}
	addr := common.HexToAddress(args.Address)
	factory := s.idx.FactoryAddress()

	tx, err := s.chain.UnfreezeAddress(ctx, factory, addr)
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}
	receipt, err := chain.WaitMined(ctx, s.chain, tx.Hash())
	if err != nil {
		return textResult(fmt.Sprintf("tx submitted (%s) but receipt unavailable: %v", tx.Hash().Hex(), err)), nil, nil
	}
	if receipt.Status != 1 {
		return textResult("transaction reverted: " + tx.Hash().Hex()), nil, nil
	}

	txHash := receipt.TxHash.Hex()
	if err := s.db.DeleteFrozenAddress(ctx, strings.ToLower(addr.Hex())); err != nil {
		return textResult(fmt.Sprintf("db error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.db.CreateEmergencyAction(ctx, "unfreeze_address", strings.ToLower(addr.Hex()), "", "", txHash); err != nil {
		return textResult(fmt.Sprintf("db audit error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.idx.RunOnce(ctx); err != nil {
		return textResult(fmt.Sprintf("indexer sync error after successful tx %s: %v", txHash, err)), nil, nil
	}
	return jsonResult(map[string]any{"tx_hash": txHash, "address": addr.Hex()})
}

func (s *Server) handleFreezeEscrow(ctx context.Context, _ *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}
	escrowID := escrow.ID

	factory := s.idx.FactoryAddress()
	tx, err := s.chain.FreezeEscrow(ctx, factory, big.NewInt(escrow.EscrowID))
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}
	receipt, err := chain.WaitMined(ctx, s.chain, tx.Hash())
	if err != nil {
		return textResult(fmt.Sprintf("tx submitted (%s) but receipt unavailable: %v", tx.Hash().Hex(), err)), nil, nil
	}
	if receipt.Status != 1 {
		return textResult("transaction reverted: " + tx.Hash().Hex()), nil, nil
	}

	txHash := receipt.TxHash.Hex()
	if err := s.db.RecordFreezeEscrowAndRevokeDCT(ctx, escrowID, escrow.EscrowAddress, txHash); err != nil {
		return textResult(fmt.Sprintf("db local recording error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.idx.RunOnce(ctx); err != nil {
		return textResult(fmt.Sprintf("indexer sync error after successful tx %s: %v", txHash, err)), nil, nil
	}
	return jsonResult(map[string]any{"tx_hash": txHash, "escrow_id": escrowID})
}

func (s *Server) handleUnfreezeEscrow(ctx context.Context, _ *mcp.CallToolRequest, args escrowIDArgs) (*mcp.CallToolResult, any, error) {
	escrow, err := s.resolveEscrowID(ctx, args.EscrowID.String())
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}
	escrowID := escrow.ID

	factory := s.idx.FactoryAddress()
	tx, err := s.chain.UnfreezeEscrow(ctx, factory, big.NewInt(escrow.EscrowID))
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}
	receipt, err := chain.WaitMined(ctx, s.chain, tx.Hash())
	if err != nil {
		return textResult(fmt.Sprintf("tx submitted (%s) but receipt unavailable: %v", tx.Hash().Hex(), err)), nil, nil
	}
	if receipt.Status != 1 {
		return textResult("transaction reverted: " + tx.Hash().Hex()), nil, nil
	}

	txHash := receipt.TxHash.Hex()
	if err := s.db.UpdateEscrowFrozen(ctx, escrowID, false); err != nil {
		return textResult(fmt.Sprintf("db error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.db.CreateEmergencyAction(ctx, "unfreeze_escrow", escrow.EscrowAddress, "", "", txHash); err != nil {
		return textResult(fmt.Sprintf("db audit error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.idx.RunOnce(ctx); err != nil {
		return textResult(fmt.Sprintf("indexer sync error after successful tx %s: %v", txHash, err)), nil, nil
	}
	return jsonResult(map[string]any{"tx_hash": txHash, "escrow_id": escrowID})
}

func (s *Server) handleEmergencyResolve(ctx context.Context, _ *mcp.CallToolRequest, args emergencyResolveArgs) (*mcp.CallToolResult, any, error) {
	escrowID, err := strconv.ParseInt(args.EscrowID.String(), 10, 64)
	if err != nil {
		return textResult(fmt.Sprintf("invalid escrow_id: %v", err)), nil, nil
	}
	bps, err := strconv.ParseUint(args.WorkerAwardBps.String(), 10, 16)
	if err != nil {
		return textResult(fmt.Sprintf("invalid worker_award_bps: %v", err)), nil, nil
	}
	if bps > 10000 {
		return textResult("worker_award_bps must be 0-10000"), nil, nil
	}

	escrow, err := s.db.GetEscrow(ctx, escrowID)
	if err != nil {
		return textResult(fmt.Sprintf("not found: %v", err)), nil, nil
	}

	factory := s.idx.FactoryAddress()
	tx, err := s.chain.EmergencyResolve(ctx, factory, big.NewInt(escrow.EscrowID), uint16(bps))
	if err != nil {
		return textResult(fmt.Sprintf("chain error: %v", err)), nil, nil
	}
	receipt, err := chain.WaitMined(ctx, s.chain, tx.Hash())
	if err != nil {
		return textResult(fmt.Sprintf("tx submitted (%s) but receipt unavailable: %v", tx.Hash().Hex(), err)), nil, nil
	}
	if receipt.Status != 1 {
		return textResult("transaction reverted: " + tx.Hash().Hex()), nil, nil
	}

	txHash := receipt.TxHash.Hex()
	if err := s.db.RecordEmergencyResolveAndRevokeDCT(ctx, escrowID, escrow.EscrowAddress, uint16(bps), txHash); err != nil {
		return textResult(fmt.Sprintf("db local recording error after successful tx %s: %v", txHash, err)), nil, nil
	}
	if err := s.idx.RunOnce(ctx); err != nil {
		return textResult(fmt.Sprintf("indexer sync error after successful tx %s: %v", txHash, err)), nil, nil
	}
	return jsonResult(map[string]any{"tx_hash": txHash, "escrow_id": escrowID, "worker_award_bps": bps})
}

func (s *Server) handleListFrozenAddresses(ctx context.Context, _ *mcp.CallToolRequest, _ emergencyListArgs) (*mcp.CallToolResult, any, error) {
	addrs, err := s.db.ListFrozenAddresses(ctx)
	if err != nil {
		return textResult(fmt.Sprintf("db error: %v", err)), nil, nil
	}
	return jsonResult(map[string]any{"frozen_addresses": addrs, "count": len(addrs)})
}

func (s *Server) handleListEmergencyActions(ctx context.Context, _ *mcp.CallToolRequest, args emergencyListArgs) (*mcp.CallToolResult, any, error) {
	limit := 50
	if args.Limit.String() != "" {
		v, err := strconv.Atoi(args.Limit.String())
		if err == nil && v > 0 {
			limit = v
		}
	}
	offset := 0
	if args.Offset.String() != "" {
		v, err := strconv.Atoi(args.Offset.String())
		if err == nil && v >= 0 {
			offset = v
		}
	}

	actions, err := s.db.ListEmergencyActions(ctx, limit, offset)
	if err != nil {
		return textResult(fmt.Sprintf("db error: %v", err)), nil, nil
	}
	return jsonResult(map[string]any{"actions": actions, "count": len(actions)})
}

func parseMilestoneIndex(s string) (uint8, error) {
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("invalid milestone_index: %w", err)
	}
	return uint8(v), nil
}

func parseProofHashHex(raw string) ([32]byte, error) {
	var out [32]byte
	if raw == "" {
		return out, nil
	}
	if !strings.HasPrefix(raw, "0x") {
		return out, errors.New("expected 0x-prefixed hex")
	}
	normalized := raw[2:]
	if len(normalized) != 64 {
		return out, fmt.Errorf("expected 32-byte hex (64 chars), got %d", len(normalized))
	}
	b, err := hex.DecodeString(normalized)
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}

func parseProofHexBytes(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("proof is required")
	}
	if !strings.HasPrefix(raw, "0x") {
		return nil, errors.New("expected 0x-prefixed hex")
	}
	normalized := raw[2:]
	if len(normalized)%2 != 0 {
		return nil, errors.New("hex length must be even")
	}
	b, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("proof is empty")
	}
	return b, nil
}

// isERC20Token returns true if the token field represents an ERC20 token (not ETH).
func isERC20Token(token string) bool {
	return token != "" && token != "0x0000000000000000000000000000000000000000"
}

// normalizeToken normalizes the canonical zero-address to an empty string (ETH).
func normalizeToken(token string) string {
	if token == "" || token == "0x0000000000000000000000000000000000000000" {
		return ""
	}
	return token
}

// hasStake returns true if the escrow has a non-zero worker stake.
func hasStake(escrow *storage.Escrow) bool {
	amt, ok := new(big.Int).SetString(escrow.WorkerStake, 10)
	return ok && amt.Sign() > 0
}

// parseOptionalMilestoneIndex converts a raw string arg into a *int milestone
// index, validating it is within [0, maxCount). Returns nil, nil for empty input.
func parseOptionalMilestoneIndex(raw string, maxCount int) (*int, error) {
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid milestone_index: %w", err)
	}
	if v < 0 || v >= maxCount {
		return nil, fmt.Errorf("milestone_index %d out of range [0, %d)", v, maxCount)
	}
	return &v, nil
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
