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
	Status                   string
	SubmissionDeadline       string
	ReviewPeriodSeconds      int64
	DisputePeriodSeconds     int64
	ArbitratorTimeoutSeconds int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
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
