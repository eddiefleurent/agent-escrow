package events

import "testing"

func TestGranularityStringAndParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want GranularityLevel
	}{
		{"L0", L0},
		{"L1", L1},
		{"L2", L2},
		{"L3", L3},
		{"unknown", L1},
	}
	for _, tc := range cases {
		if got := ParseGranularity(tc.in); got != tc.want {
			t.Fatalf("parse %q: expected %v, got %v", tc.in, tc.want, got)
		}
	}

	if L0.String() != "L0" || L1.String() != "L1" || L2.String() != "L2" || L3.String() != "L3" {
		t.Fatal("expected canonical granularity labels")
	}
	if got := GranularityLevel(99).String(); got != "unknown" {
		t.Fatalf("expected unknown label for unsupported level, got %q", got)
	}
}

func TestOnChainEventNameMappings(t *testing.T) {
	t.Parallel()

	if OnChainEventName["EscrowCreated"] != EventEscrowCreated {
		t.Fatalf("expected EscrowCreated mapping to %q", EventEscrowCreated)
	}
	if OnChainEventName["EmergencyResolved"] != EventEmergencyResolved {
		t.Fatalf("expected EmergencyResolved mapping to %q", EventEmergencyResolved)
	}
	if OnChainEventName["EmergencyFrozen"] != EventEscrowEmergFrozen {
		t.Fatalf("expected EmergencyFrozen mapping to %q", EventEscrowEmergFrozen)
	}
}
