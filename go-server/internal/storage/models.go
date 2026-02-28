package storage

import "time"

type Task struct {
	ID          int64
	Title       string
	Description string
	SpecHash    string
	CreatedAt   time.Time
}

type Escrow struct {
	ID                       int64
	TaskID                   int64
	ChainID                  int64
	FactoryAddress           string
	EscrowAddress            string
	EscrowID                 int64
	Buyer                    string
	Worker                   string
	Verifier                 string // Legacy single-verifier mirror (first panel member) for backward compatibility in older RFQ flows
	VerifierPanelJSON        string `json:"verifier_panel_json"`
	QuorumThreshold          int    `json:"quorum_threshold"`
	QuorumVerifierCount      int    `json:"quorum_verifier_count"`
	VerifierStakePerVerifier string `json:"verifier_stake_per_verifier"`
	Arbitrator               string
	Amount                   string
	WorkerStake              string // Anti-Sybil bond amount (paper §4.8); "0" means no stake required
	Token                    string // ERC20 token address; empty or "0x0000000000000000000000000000000000000000" for ETH
	Status                   string
	SubmissionDeadline       int64
	ReviewPeriodSeconds      int64
	DisputePeriodSeconds     int64
	ArbitratorTimeoutSeconds int64
	MilestoneCount           int    `json:"milestone_count"`
	CurrentMilestone         int    `json:"current_milestone"`
	BackupWorker             string `json:"backup_worker"`
	BackupDeadlineExtension  int64  `json:"backup_deadline_extension"`
	ActiveWorker             string `json:"active_worker"`
	BackupActivated          bool   `json:"backup_activated"`
	Frozen                   bool   `json:"frozen"`
	ServiceTier              int    `json:"service_tier"` // 0 = low_assurance, 1 = high_assurance (paper §5.3)
	ZKVerifier               string `json:"zk_verifier"`  // Optional on-chain verifier contract; zero address means disabled
	CircuitID                string `json:"circuit_id"`   // bytes32 circuit identifier used by zkVerifier
	ParentEscrowID           *int64 `json:"parent_escrow_id,omitempty"`
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type MilestoneRecord struct {
	ID                 int64
	EscrowID           int64
	MilestoneIndex     int
	Amount             string
	SubmissionDeadline int64
	Status             string
	SubmissionHash     string
	SubmissionURI      string
	ProofHash          string
	SubmittedAt        *time.Time
	ApprovedAt         *time.Time
	DisputedAt         *time.Time
	DisputeReasonURI   string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Submission struct {
	ID             int64
	EscrowID       int64
	SubmissionHash string
	SubmissionURI  string
	ProofHash      string
	SubmittedAt    time.Time
}

type Dispute struct {
	ID             int64
	EscrowID       int64
	RaisedBy       string
	ReasonURI      string
	ResolutionURI  string
	WorkerAwardBps *int
	Status         string
	CreatedAt      time.Time
	ResolvedAt     *time.Time
}

type Reputation struct {
	ID        int64     `json:"id"`
	Address   string    `json:"address"`
	Role      string    `json:"role"`
	Completed int       `json:"completed"`
	Disputed  int       `json:"disputed"`
	Failed    int       `json:"failed"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RFQ represents a Task Request for Quote broadcast (paper §6.1).
type RFQ struct {
	ID                       int64     `json:"id"`
	Title                    string    `json:"title"`
	Description              string    `json:"description"`
	SpecHash                 string    `json:"spec_hash"`
	Buyer                    string    `json:"buyer"`
	Token                    string    `json:"token"`
	BudgetMin                string    `json:"budget_min"`
	BudgetMax                string    `json:"budget_max"`
	Deadline                 int64     `json:"deadline"`
	ReviewPeriodSeconds      int64     `json:"review_period_seconds"`
	DisputePeriodSeconds     int64     `json:"dispute_period_seconds"`
	ArbitratorTimeoutSeconds int64     `json:"arbitrator_timeout_seconds"`
	Verifier                 string    `json:"verifier"`
	Arbitrator               string    `json:"arbitrator"`
	WorkerStake              string    `json:"worker_stake"`
	MilestonesJSON           string    `json:"milestones_json"`
	RequirementsJSON         string    `json:"requirements_json"`
	RequiredCredentialsJSON  string    `json:"required_credentials_json"`
	BiddingMode              string    `json:"bidding_mode"`
	CommitDeadline           int64     `json:"commit_deadline"`
	RevealDeadline           int64     `json:"reveal_deadline"`
	ServiceTier              int       `json:"service_tier"` // 0 = low_assurance, 1 = high_assurance (paper §5.3)
	ParentEscrowID           *int64    `json:"parent_escrow_id,omitempty"`
	Status                   string    `json:"status"`
	ExpiresAt                int64     `json:"expires_at"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// Bid represents a signed Bid_Object from a worker agent (paper §6.1).
type Bid struct {
	ID                     int64     `json:"id"`
	RFQID                  int64     `json:"rfq_id"`
	Bidder                 string    `json:"bidder"`
	Amount                 string    `json:"amount"`
	EstimatedDuration      int64     `json:"estimated_duration"`
	ReputationBond         string    `json:"reputation_bond"`
	MilestonesJSON         string    `json:"milestones_json"`
	Message                string    `json:"message"`
	Status                 string    `json:"status"`
	EscrowID               *int64    `json:"escrow_id"`
	ExpiresAt              int64     `json:"expires_at"`
	StakeMandateID         string    `json:"stake_mandate_id,omitempty"` // AP2 mandate for Sybil-resistant stake-on-bid (paper §6)
	CredentialsJSON        string    `json:"credentials_json"`
	CredentialVerified     bool      `json:"credential_verified"`
	CredentialMatchSummary string    `json:"credential_match_summary"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// BidCommit represents a sealed-bid commitment before reveal.
type BidCommit struct {
	ID            int64     `json:"id"`
	RFQID         int64     `json:"rfq_id"`
	Bidder        string    `json:"bidder"`
	Commitment    string    `json:"commitment"`
	Nonce         string    `json:"nonce"`
	Status        string    `json:"status"`
	RevealedBidID *int64    `json:"revealed_bid_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// A2ATask represents an A2A protocol task linked to an escrow (paper §6: A2A Task object extension).
type A2ATask struct {
	ID                     int64
	A2ATaskID              string
	SessionID              string
	EscrowID               *int64
	DelegatorAgent         string
	DelegateeAgent         string
	VerificationPolicyJSON string
	EscrowTrigger          bool
	A2AStatus              string // submitted, working, input-required, completed, failed, canceled
	MetadataJSON           string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// AP2Mandate represents an AP2 mandate linked to an escrow (paper §6: AP2 stake-on-bid).
type AP2Mandate struct {
	ID             string    `json:"id"`
	MandateType    string    `json:"mandate_type"`
	MandateHash    string    `json:"mandate_hash"`
	SignerAddress  string    `json:"signer_address"`
	BudgetAmount   string    `json:"budget_amount,omitempty"`
	BudgetCurrency string    `json:"budget_currency,omitempty"`
	ExpiresAt      string    `json:"expires_at,omitempty"`
	EscrowID       *int64    `json:"escrow_id,omitempty"`
	FundingTxHash  string    `json:"funding_tx_hash,omitempty"`
	Status         string    `json:"status"`
	RawPayload     string    `json:"raw_payload"`
	CreatedAt      time.Time `json:"created_at"`
}

// FrozenAddress represents a frozen address in the emergency protocol.
type FrozenAddress struct {
	Address  string    `json:"address"`
	FrozenAt time.Time `json:"frozen_at"`
	Reason   string    `json:"reason"`
	FrozenBy string    `json:"frozen_by"`
}

// EmergencyAction represents an audit log entry for emergency actions.
type EmergencyAction struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	EscrowID  string    `json:"escrow_id"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	TxHash    string    `json:"tx_hash"`
}

type ChainLog struct {
	ID              int64
	TxHash          string
	LogIndex        int
	BlockNumber     int64
	EventName       string
	ContractAddress string
	RawData         string
	CreatedAt       time.Time
}

type ChainCursor struct {
	ID          int64
	ChainID     int64
	CursorKey   string
	BlockNumber int64
	UpdatedAt   time.Time
}

// AttestationChain is submission-scoped metadata for a chain of signed completion attestations (paper §4.8).
type AttestationChain struct {
	ID                      int64     `json:"id"`
	EscrowID                int64     `json:"escrow_id"`
	MilestoneIndex          *int      `json:"milestone_index,omitempty"`
	RootHash                string    `json:"root_hash"`
	Verified                bool      `json:"verified"`
	VerificationSummaryJSON string    `json:"verification_summary_json"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// AttestationLink is a single signed link in an attestation chain (paper §4.8).
type AttestationLink struct {
	ID            int64     `json:"id"`
	ChainID       int64     `json:"chain_id"`
	LinkID        string    `json:"link_id"`
	ParentLinkID  string    `json:"parent_link_id,omitempty"`
	FromAddress   string    `json:"from_address"`
	ToAddress     string    `json:"to_address"`
	ChildEscrowID *int64    `json:"child_escrow_id,omitempty"`
	TaskSpecHash  string    `json:"task_spec_hash,omitempty"`
	OutcomeHash   string    `json:"outcome_hash,omitempty"`
	IssuedAt      int64     `json:"issued_at"`
	ExpiresAt     int64     `json:"expires_at"`
	Nonce         string    `json:"nonce"`
	Signature     string    `json:"signature"`
	PayloadJSON   string    `json:"payload_json"`
	CreatedAt     time.Time `json:"created_at"`
}

// Checkpoint is a standardized state snapshot committed by the active worker for mid-task agent swaps (paper §6.1).
type Checkpoint struct {
	ID               int64     `json:"id"`
	EscrowID         int64     `json:"escrow_id"`
	MilestoneIndex   *int      `json:"milestone_index,omitempty"`
	StateSnapshotURI string    `json:"state_snapshot_uri"`
	SnapshotHash     string    `json:"snapshot_hash,omitempty"`
	SchemaVersion    string    `json:"schema_version"`
	CommittedBy      string    `json:"committed_by"`
	CompletionPct    *int      `json:"completion_pct,omitempty"`
	MetadataJSON     string    `json:"metadata_json"`
	CreatedAt        time.Time `json:"created_at"`
}

// DCTToken is an off-chain Delegation Capability Token record (paper §4.7, §6.1).
type DCTToken struct {
	ID               int64      `json:"id"`
	TokenID          string     `json:"token_id"`
	TokenHash        string     `json:"-"`
	ParentTokenID    string     `json:"parent_token_id,omitempty"`
	EscrowID         int64      `json:"escrow_id"`
	Subject          string     `json:"subject"`
	Issuer           string     `json:"issuer"`
	OperationsJSON   string     `json:"operations_json"`
	ResourcesJSON    string     `json:"resources_json"`
	Profile          string     `json:"profile"`
	CaveatsJSON      string     `json:"caveats_json"`
	Depth            int        `json:"depth"`
	ExpiresAt        int64      `json:"expires_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	RevocationReason string     `json:"revocation_reason,omitempty"`
	RevokedBy        string     `json:"revoked_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
