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
	Verifier                 string
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
	Status                   string    `json:"status"`
	ExpiresAt                int64     `json:"expires_at"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// Bid represents a signed Bid_Object from a worker agent (paper §6.1).
type Bid struct {
	ID                int64     `json:"id"`
	RFQID             int64     `json:"rfq_id"`
	Bidder            string    `json:"bidder"`
	Amount            string    `json:"amount"`
	EstimatedDuration int64     `json:"estimated_duration"`
	ReputationBond    string    `json:"reputation_bond"`
	MilestonesJSON    string    `json:"milestones_json"`
	Message           string    `json:"message"`
	Status            string    `json:"status"`
	EscrowID          *int64    `json:"escrow_id"`
	ExpiresAt         int64     `json:"expires_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
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
