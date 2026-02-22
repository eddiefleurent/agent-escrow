package events

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	bus := NewEventBus(16)
	sub := bus.Subscribe(L1, "")
	defer bus.Unsubscribe(sub.ID)

	event := Event{
		Name:   EventEscrowFunded,
		Escrow: "0xABC",
		Level:  L1,
		ID:     "test-1",
	}
	bus.Publish(event)

	select {
	case got := <-sub.Ch:
		if got.Name != EventEscrowFunded {
			t.Errorf("expected event name %q, got %q", EventEscrowFunded, got.Name)
		}
		if got.Escrow != "0xABC" {
			t.Errorf("expected escrow 0xABC, got %q", got.Escrow)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestGranularityFiltering(t *testing.T) {
	bus := NewEventBus(16)

	subL0 := bus.Subscribe(L0, "")
	defer bus.Unsubscribe(subL0.ID)

	subL1 := bus.Subscribe(L1, "")
	defer bus.Unsubscribe(subL1.ID)

	heartbeat := Event{Name: EventHeartbeat, Level: L0, ID: "hb-1"}
	funded := Event{Name: EventEscrowFunded, Level: L1, Escrow: "0xABC", ID: "fund-1"}

	bus.Publish(heartbeat)
	bus.Publish(funded)

	// L0 subscriber should only get heartbeat
	select {
	case got := <-subL0.Ch:
		if got.Name != EventHeartbeat {
			t.Errorf("L0 sub: expected heartbeat, got %q", got.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("L0 sub: timed out waiting for heartbeat")
	}

	select {
	case got := <-subL0.Ch:
		t.Errorf("L0 sub: should not receive L1 event, got %q", got.Name)
	case <-time.After(50 * time.Millisecond):
		// Expected: no more events for L0 subscriber
	}

	// L1 subscriber should get both
	var l1Events []Event
	for i := 0; i < 2; i++ {
		select {
		case got := <-subL1.Ch:
			l1Events = append(l1Events, got)
		case <-time.After(time.Second):
			t.Fatalf("L1 sub: timed out waiting for event %d", i)
		}
	}
	if len(l1Events) != 2 {
		t.Fatalf("L1 sub: expected 2 events, got %d", len(l1Events))
	}
	if l1Events[0].Name != EventHeartbeat {
		t.Errorf("L1 sub: first event should be heartbeat, got %q", l1Events[0].Name)
	}
	if l1Events[1].Name != EventEscrowFunded {
		t.Errorf("L1 sub: second event should be escrow.funded, got %q", l1Events[1].Name)
	}
}

func TestEscrowFiltering(t *testing.T) {
	bus := NewEventBus(16)

	subAll := bus.Subscribe(L1, "*")
	defer bus.Unsubscribe(subAll.ID)

	subSpecific := bus.Subscribe(L1, "0xABC")
	defer bus.Unsubscribe(subSpecific.ID)

	bus.Publish(Event{Name: EventEscrowFunded, Escrow: "0xABC", Level: L1, ID: "e1"})
	bus.Publish(Event{Name: EventEscrowFunded, Escrow: "0xDEF", Level: L1, ID: "e2"})

	// All-escrow subscriber gets both
	for i := 0; i < 2; i++ {
		select {
		case <-subAll.Ch:
		case <-time.After(time.Second):
			t.Fatalf("all-escrow sub: timed out on event %d", i)
		}
	}

	// Specific subscriber gets only 0xABC
	select {
	case got := <-subSpecific.Ch:
		if got.Escrow != "0xABC" {
			t.Errorf("specific sub: expected 0xABC, got %q", got.Escrow)
		}
	case <-time.After(time.Second):
		t.Fatal("specific sub: timed out")
	}

	select {
	case got := <-subSpecific.Ch:
		t.Errorf("specific sub: should not receive 0xDEF event, got escrow=%q", got.Escrow)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := NewEventBus(16)
	sub := bus.Subscribe(L1, "")

	if bus.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", bus.SubscriberCount())
	}

	bus.Unsubscribe(sub.ID)

	if bus.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers after unsubscribe, got %d", bus.SubscriberCount())
	}

	// Channel should be closed
	_, ok := <-sub.Ch
	if ok {
		t.Error("expected channel to be closed after unsubscribe")
	}
}

func TestBufferOverflow(t *testing.T) {
	bus := NewEventBus(2)
	sub := bus.Subscribe(L1, "")
	defer bus.Unsubscribe(sub.ID)

	// Publish 5 events without reading -- only first 2 should be buffered
	for i := 0; i < 5; i++ {
		bus.Publish(Event{Name: EventEscrowFunded, Level: L1, ID: itoa(uint64(i))})
	}

	var received int
	for {
		select {
		case <-sub.Ch:
			received++
		case <-time.After(50 * time.Millisecond):
			goto done
		}
	}
done:
	if received != 2 {
		t.Errorf("expected 2 buffered events (buffer size), got %d", received)
	}
}

func TestConcurrentAccess(t *testing.T) {
	bus := NewEventBus(64)
	const numSubscribers = 10
	const numEvents = 100

	var subs []*Subscription
	for i := 0; i < numSubscribers; i++ {
		subs = append(subs, bus.Subscribe(L1, ""))
	}

	var wg sync.WaitGroup

	// Concurrent publishers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numEvents; j++ {
				bus.Publish(Event{
					Name:  EventEscrowFunded,
					Level: L1,
					ID:    itoa(uint64(workerID*numEvents + j)),
				})
			}
		}(i)
	}

	// Concurrent unsubscribers (unsubscribe half the subscribers)
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		for i := 0; i < numSubscribers/2; i++ {
			bus.Unsubscribe(subs[i].ID)
		}
	}()

	wg.Wait()

	// Remaining subscribers should still be functional
	remaining := bus.SubscriberCount()
	if remaining != numSubscribers/2 {
		t.Errorf("expected %d remaining subscribers, got %d", numSubscribers/2, remaining)
	}

	// Clean up
	for i := numSubscribers / 2; i < numSubscribers; i++ {
		bus.Unsubscribe(subs[i].ID)
	}
}

func TestHeartbeat(t *testing.T) {
	bus := NewEventBus(16)
	sub := bus.Subscribe(L0, "")
	defer bus.Unsubscribe(sub.ID)

	ctx, cancel := context.WithCancel(context.Background())
	go bus.RunHeartbeat(ctx, 50*time.Millisecond)

	select {
	case got := <-sub.Ch:
		if got.Name != EventHeartbeat {
			t.Errorf("expected heartbeat, got %q", got.Name)
		}
		if got.Level != L0 {
			t.Errorf("expected L0 level, got %v", got.Level)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for heartbeat")
	}

	cancel()
}

func TestRecentEvents(t *testing.T) {
	bus := NewEventBus(16)

	for i := 0; i < 5; i++ {
		bus.Publish(Event{
			Name:   EventEscrowFunded,
			Escrow: "0xABC",
			Level:  L1,
			ID:     itoa(uint64(i)),
		})
	}

	events := bus.RecentEvents("", L1, "", 10)
	if len(events) != 5 {
		t.Fatalf("expected 5 recent events, got %d", len(events))
	}

	// Filter by sinceID
	events = bus.RecentEvents("", L1, "2", 10)
	if len(events) != 2 {
		t.Fatalf("expected 2 events after ID=2, got %d", len(events))
	}
	if events[0].ID != "3" {
		t.Errorf("expected first event ID=3, got %q", events[0].ID)
	}

	// Filter by escrow
	bus.Publish(Event{Name: EventEscrowFunded, Escrow: "0xDEF", Level: L1, ID: "other"})
	events = bus.RecentEvents("0xABC", L1, "", 10)
	if len(events) != 5 {
		t.Errorf("expected 5 events for 0xABC, got %d", len(events))
	}

	// Limit
	events = bus.RecentEvents("", L1, "", 3)
	if len(events) != 3 {
		t.Errorf("expected 3 events with limit=3, got %d", len(events))
	}
}

func TestRecentEventsGranularityFilter(t *testing.T) {
	bus := NewEventBus(16)

	bus.Publish(Event{Name: EventHeartbeat, Level: L0, ID: "hb"})
	bus.Publish(Event{Name: EventEscrowFunded, Level: L1, ID: "fund"})

	events := bus.RecentEvents("", L0, "", 10)
	if len(events) != 1 {
		t.Fatalf("expected 1 L0 event, got %d", len(events))
	}
	if events[0].Name != EventHeartbeat {
		t.Errorf("expected heartbeat, got %q", events[0].Name)
	}
}

func TestParseGranularity(t *testing.T) {
	tests := []struct {
		input string
		want  GranularityLevel
	}{
		{"L0", L0},
		{"L1", L1},
		{"L2", L2},
		{"L3", L3},
		{"", L1},
		{"invalid", L1},
	}
	for _, tt := range tests {
		got := ParseGranularity(tt.input)
		if got != tt.want {
			t.Errorf("ParseGranularity(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestOnChainEventNameMapping(t *testing.T) {
	expected := map[string]string{
		"EscrowCreated":   EventEscrowCreated,
		"EscrowFunded":    EventEscrowFunded,
		"SubmissionMade":  EventEscrowSubmitted,
		"Approved":        EventEscrowApproved,
		"Disputed":        EventEscrowDisputed,
		"Settled":         EventEscrowSettled,
		"OutcomeRecorded": EventOutcomeRecorded,
		"BackupActivated": EventBackupActivated,
	}
	for onChain, streamName := range expected {
		got, ok := OnChainEventName[onChain]
		if !ok {
			t.Errorf("missing mapping for on-chain event %q", onChain)
			continue
		}
		if got != streamName {
			t.Errorf("OnChainEventName[%q] = %q, want %q", onChain, got, streamName)
		}
	}
}

func TestTimestampAutoSet(t *testing.T) {
	bus := NewEventBus(16)
	sub := bus.Subscribe(L1, "")
	defer bus.Unsubscribe(sub.ID)

	before := time.Now().UTC()
	bus.Publish(Event{Name: EventEscrowFunded, Level: L1, ID: "ts-test"})
	after := time.Now().UTC()

	select {
	case got := <-sub.Ch:
		if got.Timestamp.Before(before) || got.Timestamp.After(after) {
			t.Errorf("auto-set timestamp %v not in range [%v, %v]", got.Timestamp, before, after)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestGlobalEventsDeliveredToEscrowSubscribers(t *testing.T) {
	bus := NewEventBus(16)
	sub := bus.Subscribe(L0, "0xABC")
	defer bus.Unsubscribe(sub.ID)

	// Heartbeat has no escrow -- should still be delivered to escrow-scoped subscribers
	bus.Publish(Event{Name: EventHeartbeat, Level: L0, ID: "hb-global"})

	select {
	case got := <-sub.Ch:
		if got.Name != EventHeartbeat {
			t.Errorf("expected heartbeat, got %q", got.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("escrow-scoped subscriber should receive global events")
	}
}
