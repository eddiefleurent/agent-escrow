package events

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// EventBus is an in-process pub/sub for escrow lifecycle events.
// Publishers call Publish; subscribers receive on a buffered channel
// with granularity and escrow-address filtering applied at delivery time.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscription
	nextID      atomic.Uint64
	bufferSize  int

	// recentMu guards the recent event ring buffer used by the MCP polling tool.
	recentMu     sync.RWMutex
	recentEvents []Event
	maxRecent    int
}

// NewEventBus creates an event bus with the given per-subscriber channel buffer size.
func NewEventBus(bufferSize int) *EventBus {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	return &EventBus{
		subscribers: make(map[string]*Subscription),
		bufferSize:  bufferSize,
		maxRecent:   1000,
	}
}

// Subscribe creates a new subscription filtered by granularity level and escrow address.
// The caller must eventually call Unsubscribe to prevent resource leaks.
func (b *EventBus) Subscribe(maxLevel GranularityLevel, escrow string) *Subscription {
	id := b.nextID.Add(1)
	sub := &Subscription{
		ID:       subID(id),
		Ch:       make(chan Event, b.bufferSize),
		MaxLevel: maxLevel,
		Escrow:   normalizeEscrowFilter(escrow),
	}

	b.mu.Lock()
	b.subscribers[sub.ID] = sub
	b.mu.Unlock()

	slog.Debug("event bus: new subscription",
		"sub_id", sub.ID,
		"max_level", maxLevel.String(),
		"escrow", sub.Escrow,
	)
	return sub
}

// Unsubscribe removes a subscription and closes its channel.
func (b *EventBus) Unsubscribe(id string) {
	b.mu.Lock()
	sub, ok := b.subscribers[id]
	if ok {
		delete(b.subscribers, id)
	}
	b.mu.Unlock()

	if ok {
		close(sub.Ch)
		slog.Debug("event bus: unsubscribed", "sub_id", id)
	}
}

// Publish sends an event to all matching subscribers. Events that don't match
// a subscriber's filter (granularity or escrow) are silently dropped. If a
// subscriber's channel is full, the event is dropped for that subscriber
// (non-blocking send) to prevent slow consumers from blocking publishers.
func (b *EventBus) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	b.storeRecent(event)

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subscribers {
		if !matchesFilter(sub, event) {
			continue
		}
		select {
		case sub.Ch <- event:
		default:
			slog.Warn("event bus: subscriber buffer full, dropping event",
				"sub_id", sub.ID,
				"event", event.Name,
			)
		}
	}
}

// SubscriberCount returns the current number of active subscriptions.
func (b *EventBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// RecentEvents returns up to limit events that match the given filters.
// Events are returned in chronological order. If sinceID is non-empty,
// only events after that ID are returned.
func (b *EventBus) RecentEvents(escrow string, maxLevel GranularityLevel, sinceID string, limit int) []Event {
	if limit <= 0 {
		limit = 50
	}

	escrow = normalizeEscrowFilter(escrow)

	b.recentMu.RLock()
	defer b.recentMu.RUnlock()

	var result []Event
	pastSince := sinceID == ""

	for _, ev := range b.recentEvents {
		if !pastSince {
			if ev.ID == sinceID {
				pastSince = true
			}
			continue
		}

		if ev.Level > maxLevel {
			continue
		}
		if escrow != "" && ev.Escrow != "" && ev.Escrow != escrow {
			continue
		}

		result = append(result, ev)
		if len(result) >= limit {
			break
		}
	}

	return result
}

// RunHeartbeat starts a goroutine that publishes L0 heartbeat events at the
// given interval. It stops when the context is cancelled.
func (b *EventBus) RunHeartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.Publish(Event{
				Name:      EventHeartbeat,
				Level:     L0,
				Timestamp: time.Now().UTC(),
				ID:        "heartbeat-" + time.Now().UTC().Format("20060102T150405Z"),
			})
		}
	}
}

func (b *EventBus) storeRecent(event Event) {
	b.recentMu.Lock()
	defer b.recentMu.Unlock()

	b.recentEvents = append(b.recentEvents, event)
	if len(b.recentEvents) > b.maxRecent {
		// Drop oldest events beyond the ring buffer limit.
		excess := len(b.recentEvents) - b.maxRecent
		b.recentEvents = b.recentEvents[excess:]
	}
}

func matchesFilter(sub *Subscription, event Event) bool {
	if event.Level > sub.MaxLevel {
		return false
	}
	if sub.Escrow != "" && event.Escrow != "" && event.Escrow != sub.Escrow {
		return false
	}
	return true
}

func normalizeEscrowFilter(escrow string) string {
	if escrow == "*" || escrow == "" {
		return ""
	}
	return escrow
}

func subID(n uint64) string {
	return "sub-" + itoa(n)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
