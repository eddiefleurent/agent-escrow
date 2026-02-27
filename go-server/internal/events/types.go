package events

import (
	"time"
)

// GranularityLevel represents the paper's configurable monitoring granularity
// (Section 4.5, Section 6.1). Subscribers declare a maximum level; the bus
// delivers events at or below that level.
type GranularityLevel int

const (
	L0 GranularityLevel = iota // IS_OPERATIONAL: heartbeat/liveness
	L1                         // HIGH_LEVEL_PLAN_UPDATES: state transitions
	L2                         // COT_TRACE: chain-of-thought reasoning (future)
	L3                         // FULL_STATE: complete state snapshots (future)
)

func (g GranularityLevel) String() string {
	switch g {
	case L0:
		return "L0"
	case L1:
		return "L1"
	case L2:
		return "L2"
	case L3:
		return "L3"
	default:
		return "unknown"
	}
}

// ParseGranularity converts a string like "L0", "L1", etc. to a GranularityLevel.
// Returns L1 as default for unrecognized input.
func ParseGranularity(s string) GranularityLevel {
	switch s {
	case "L0":
		return L0
	case "L1":
		return L1
	case "L2":
		return L2
	case "L3":
		return L3
	default:
		return L1
	}
}

// Event represents a lifecycle event published through the bus.
type Event struct {
	// Name is the stream event name (e.g. "escrow.funded", "heartbeat").
	Name string `json:"event"`

	// Escrow is the on-chain escrow address this event relates to.
	// Empty for global events (heartbeat).
	Escrow string `json:"escrow,omitempty"`

	// Level is the granularity level of this event.
	Level GranularityLevel `json:"level"`

	// Block is the chain block number associated with the event (0 for off-chain events).
	Block uint64 `json:"block,omitempty"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// ID uniquely identifies this event (e.g. "0x<txHash>-<logIndex>" for on-chain events).
	ID string `json:"id"`

	// Payload carries event-specific data.
	Payload map[string]any `json:"payload,omitempty"`
}

// Subscription represents a client's subscription to the event bus.
type Subscription struct {
	// ID uniquely identifies this subscription.
	ID string

	// Ch receives events matching the subscription filter.
	Ch chan Event

	// MaxLevel is the maximum granularity level to receive.
	// Events with Level > MaxLevel are filtered out.
	MaxLevel GranularityLevel

	// Escrow filters events to a specific escrow address.
	// Empty string ("" or "*") means all escrows.
	Escrow string
}

// Standard event names mapping on-chain events to stream event names.
const (
	EventHeartbeat                 = "heartbeat"
	EventEscrowCreated             = "escrow.created"
	EventEscrowFunded              = "escrow.funded"
	EventEscrowSubmitted           = "escrow.submitted"
	EventEscrowApproved            = "escrow.approved"
	EventEscrowRejected            = "escrow.rejected"
	EventEscrowDisputed            = "escrow.disputed"
	EventSilenceEscalated          = "escrow.silence_escalated"
	EventDisputeResolved           = "escrow.dispute_resolved"
	EventEscrowSettled             = "escrow.settled"
	EventEscrowRefunded            = "escrow.refunded"
	EventEscrowCancelled           = "escrow.cancelled"
	EventBackupActivated           = "escrow.backup_activated"
	EventMilestoneSubmitted        = "milestone.submitted"
	EventMilestoneApproved         = "milestone.approved"
	EventMilestoneDisputed         = "milestone.disputed"
	EventMilestoneDisResolved      = "milestone.dispute_resolved"
	EventMilestoneSettled          = "milestone.settled"
	EventMilestoneCancelled        = "milestone.cancelled"
	EventMilestoneAborted          = "milestone.aborted"
	EventOutcomeRecorded           = "reputation.outcome_recorded"
	EventWorkerStakeDeposited      = "escrow.stake_deposited"
	EventAttestationChainSubmitted = "attestation.chain_submitted"
	EventAttestationChainVerified  = "attestation.chain_verified"
	EventAddressFrozen             = "emergency.address_frozen"
	EventAddressUnfrozen           = "emergency.address_unfrozen"
	EventEscrowEmergFrozen         = "emergency.escrow_frozen"
	EventEscrowEmergUnfrozen       = "emergency.escrow_unfrozen"
	EventEmergencyResolved         = "emergency.resolved"
)

// OnChainEventName maps Solidity event names to stream event names.
var OnChainEventName = map[string]string{
	"EscrowCreated":              EventEscrowCreated,
	"EscrowFunded":               EventEscrowFunded,
	"SubmissionMade":             EventEscrowSubmitted,
	"Approved":                   EventEscrowApproved,
	"Rejected":                   EventEscrowRejected,
	"Disputed":                   EventEscrowDisputed,
	"SilenceEscalated":           EventSilenceEscalated,
	"DisputeResolved":            EventDisputeResolved,
	"Settled":                    EventEscrowSettled,
	"Refunded":                   EventEscrowRefunded,
	"Cancelled":                  EventEscrowCancelled,
	"BackupActivated":            EventBackupActivated,
	"MilestoneSubmitted":         EventMilestoneSubmitted,
	"MilestoneApproved":          EventMilestoneApproved,
	"MilestoneDisputed":          EventMilestoneDisputed,
	"MilestoneDisputeResolved":   EventMilestoneDisResolved,
	"MilestoneSettled":           EventMilestoneSettled,
	"MilestoneCancelled":         EventMilestoneCancelled,
	"RemainingMilestonesAborted": EventMilestoneAborted,
	"OutcomeRecorded":            EventOutcomeRecorded,
	"WorkerStakeDeposited":       EventWorkerStakeDeposited,
	"ArbitratorTimeoutClaimed":   EventEscrowRefunded,
	"AddressFrozen":              EventAddressFrozen,
	"AddressUnfrozen":            EventAddressUnfrozen,
	"EscrowFrozen":               EventEscrowEmergFrozen,
	"EscrowUnfrozen":             EventEscrowEmergUnfrozen,
	"EmergencyResolved":          EventEmergencyResolved,
	"EmergencyFrozen":            EventEscrowEmergFrozen,
	"EmergencyUnfrozen":          EventEscrowEmergUnfrozen,
}
