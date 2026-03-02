package ucp

type CheckoutStatus string

const (
	CheckoutStatusIncomplete         CheckoutStatus = "incomplete"
	CheckoutStatusRequiresEscalation CheckoutStatus = "requires_escalation"
	CheckoutStatusReadyForComplete   CheckoutStatus = "ready_for_complete"
	CheckoutStatusCompleteInProgress CheckoutStatus = "complete_in_progress"
	CheckoutStatusCompleted          CheckoutStatus = "completed"
	CheckoutStatusCanceled           CheckoutStatus = "canceled"
)

type CreateCheckoutRequest struct {
	CheckoutID     string               `json:"checkout_id,omitempty"`
	SessionID      string               `json:"session_id,omitempty"`
	IdempotencyKey string               `json:"idempotency_key,omitempty"`
	EscrowID       *int64               `json:"escrow_id,omitempty"`
	CreateEscrow   *CreateEscrowPayload `json:"create_escrow,omitempty"`
	AutoFund       bool                 `json:"auto_fund,omitempty"`
}

type CreateEscrowPayload struct {
	Title                    string             `json:"title"`
	Description              string             `json:"description"`
	Buyer                    string             `json:"buyer"`
	Worker                   string             `json:"worker"`
	VerifierPanel            []string           `json:"verifier_panel"`
	QuorumThreshold          int                `json:"quorum_threshold"`
	QuorumVerifierCount      int                `json:"quorum_verifier_count"`
	VerifierStakePerVerifier string             `json:"verifier_stake_per_verifier,omitempty"`
	Arbitrator               string             `json:"arbitrator"`
	Amount                   string             `json:"amount"`
	WorkerStake              string             `json:"worker_stake,omitempty"`
	SubmissionDeadline       string             `json:"submission_deadline"`
	ReviewPeriodSeconds      string             `json:"review_period_seconds"`
	DisputePeriodSeconds     string             `json:"dispute_period_seconds"`
	ArbitratorTimeoutSeconds string             `json:"arbitrator_timeout_seconds"`
	Token                    string             `json:"token,omitempty"`
	ServiceTier              int                `json:"service_tier,omitempty"`
	Milestones               []milestonePayload `json:"milestones,omitempty"`
	BackupWorker             string             `json:"backup_worker,omitempty"`
	BackupDeadlineExtension  string             `json:"backup_deadline_extension,omitempty"`
	ZKVerifier               string             `json:"zk_verifier,omitempty"`
	CircuitID                string             `json:"circuit_id,omitempty"`
	ParentEscrowID           *int64             `json:"parent_escrow_id,omitempty"`
}

type milestonePayload struct {
	Amount             string `json:"amount"`
	SubmissionDeadline string `json:"submission_deadline"`
}

type UpdateCheckoutRequest struct {
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Operation      string `json:"operation"`

	Role           string `json:"role,omitempty"`
	SubmissionURI  string `json:"submission_uri,omitempty"`
	ProofHash      string `json:"proof_hash,omitempty"`
	Proof          string `json:"proof,omitempty"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`

	Approve        *bool   `json:"approve,omitempty"`
	ReasonURI      string  `json:"reason_uri,omitempty"`
	WorkerAwardBps *uint16 `json:"worker_award_bps,omitempty"`
	ResolutionURI  string  `json:"resolution_uri,omitempty"`
}

type CompleteCheckoutRequest struct {
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Role           string `json:"role,omitempty"`
	Proof          string `json:"proof,omitempty"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
}

type CancelCheckoutRequest struct {
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	MilestoneIndex *int   `json:"milestone_index,omitempty"`
}

type Checkout struct {
	CheckoutID    string         `json:"checkout_id"`
	SessionID     string         `json:"session_id"`
	EscrowID      int64          `json:"escrow_id"`
	UCPStatus     CheckoutStatus `json:"ucp_status"`
	EscrowStatus  string         `json:"escrow_status"`
	LastOperation string         `json:"last_operation,omitempty"`
	LastTxHash    string         `json:"last_tx_hash,omitempty"`
	NextAction    string         `json:"next_action,omitempty"`
	Escrow        any            `json:"escrow,omitempty"`
}

type WellKnownProfile struct {
	Version      string            `json:"version"`
	ProviderName string            `json:"provider_name"`
	ProviderURL  string            `json:"provider_url"`
	Operations   []string          `json:"operations"`
	Endpoints    map[string]string `json:"endpoints"`
	StatusMap    map[string]string `json:"status_map"`
}
