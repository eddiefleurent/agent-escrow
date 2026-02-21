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
