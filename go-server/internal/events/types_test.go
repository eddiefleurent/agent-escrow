package events

import "testing"

func TestGranularityStringAndParse(t *testing.T) {
	t.Parallel()

	parseCases := []struct {
		in   string
		want GranularityLevel
	}{
		{in: "L0", want: L0},
		{in: "L1", want: L1},
		{in: "L2", want: L2},
		{in: "L3", want: L3},
		{in: "", want: L1},
		{in: "invalid", want: L1},
		{in: "unknown", want: L1},
	}
	for _, tc := range parseCases {
		if got := ParseGranularity(tc.in); got != tc.want {
			t.Errorf("ParseGranularity(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	stringCases := []struct {
		level GranularityLevel
		want  string
	}{
		{level: L0, want: "L0"},
		{level: L1, want: "L1"},
		{level: L2, want: "L2"},
		{level: L3, want: "L3"},
		{level: GranularityLevel(99), want: "unknown"},
	}
	for _, tc := range stringCases {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("GranularityLevel(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestOnChainEventNameMappings(t *testing.T) {
	t.Parallel()

	want := map[string]string{
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
		"MarketStabilityFeeApplied":  EventMarketStabilityFeeApplied,
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

	if len(OnChainEventName) != len(want) {
		t.Fatalf("OnChainEventName has %d keys, want %d", len(OnChainEventName), len(want))
	}
	for key, streamName := range want {
		got, ok := OnChainEventName[key]
		if !ok {
			t.Errorf("missing mapping for on-chain event %q", key)
			continue
		}
		if got != streamName {
			t.Errorf("OnChainEventName[%q] = %q, want %q", key, got, streamName)
		}
	}
}
