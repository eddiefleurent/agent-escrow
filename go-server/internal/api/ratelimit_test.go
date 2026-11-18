package api

import (
	"bytes"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestLimiter(rps float64, burst, maxBuckets int) (*ipRateLimiter, *time.Time) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newIPRateLimiter(rps, burst, maxBuckets)
	limiter.now = func() time.Time { return now }
	return limiter, &now
}

func TestIPRateLimiterReclaimsRefilledBuckets(t *testing.T) {
	t.Parallel()

	limiter, now := newTestLimiter(10, 1, 10)

	if ok, _ := limiter.allow("old"); !ok {
		t.Fatal("expected the first client to be allowed")
	}
	*now = now.Add(6 * time.Second)
	if ok, _ := limiter.allow("new"); !ok {
		t.Fatal("expected a later client to be allowed")
	}
	if _, ok := limiter.buckets["old"]; ok {
		t.Fatal("expected the refilled bucket to be reclaimed")
	}
	if _, ok := limiter.buckets["new"]; !ok {
		t.Fatal("expected the active bucket to remain")
	}
}

// A depleted bucket must survive any amount of idleness short of a full
// refill. The previous fixed ten-minute TTL handed this client a fresh token
// after 601 seconds even though refill takes 1,000.
func TestIPRateLimiterKeepsDepletedBucketUntilRefilled(t *testing.T) {
	t.Parallel()

	limiter, now := newTestLimiter(0.001, 1, 10)

	if ok, _ := limiter.allow("slow"); !ok {
		t.Fatal("expected the burst token")
	}
	*now = now.Add(601 * time.Second)
	// Unrelated traffic drives the sweep over the depleted bucket.
	for i := range 20 {
		limiter.allow("other-" + strconv.Itoa(i%3))
	}
	if ok, _ := limiter.allow("slow"); ok {
		t.Fatal("depleted bucket was reset before it refilled")
	}
	*now = now.Add(400 * time.Second)
	if ok, _ := limiter.allow("slow"); !ok {
		t.Fatal("expected the token back after a full refill period")
	}
}

// At capacity, a new key must not evict a client that has spent its quota,
// and must not receive a burst of its own.
func TestIPRateLimiterPreservesQuotaAtCapacity(t *testing.T) {
	t.Parallel()

	limiter, now := newTestLimiter(0.001, 1, 2)

	for _, key := range []string{"a", "b"} {
		if ok, _ := limiter.allow(key); !ok {
			t.Fatalf("expected %s to be allowed", key)
		}
		*now = now.Add(time.Millisecond)
	}
	if ok, _ := limiter.allow("c"); !ok {
		t.Fatal("expected the overflow bucket to admit the first unseen key")
	}
	if len(limiter.buckets) != 2 {
		t.Fatalf("expected the table to stay at 2 buckets, got %d", len(limiter.buckets))
	}
	for _, key := range []string{"a", "b"} {
		if _, ok := limiter.buckets[key]; !ok {
			t.Fatalf("depleted bucket %s was evicted at capacity", key)
		}
		if ok, _ := limiter.allow(key); ok {
			t.Fatalf("%s regained quota after an unseen key arrived", key)
		}
	}
}

// Rotating source addresses at capacity shares one overflow budget.
func TestIPRateLimiterRotationAtCapacityBoundedByOverflow(t *testing.T) {
	t.Parallel()

	const burst = 3
	limiter, now := newTestLimiter(0.001, burst, 2)
	limiter.allow("tracked-a")
	limiter.allow("tracked-b")

	allowed := 0
	for i := range 500 {
		*now = now.Add(time.Millisecond)
		if ok, _ := limiter.allow("rotating-" + strconv.Itoa(i)); ok {
			allowed++
		}
	}
	if allowed != burst {
		t.Fatalf("expected rotation at capacity to be held to the %d-token overflow burst, got %d", burst, allowed)
	}
	if len(limiter.buckets) != 2 {
		t.Fatalf("expected the table to stay bounded at 2, got %d", len(limiter.buckets))
	}
}

// Each request inspects a bounded number of buckets, so a full table of
// active clients costs O(1) per request rather than a scan.
func TestIPRateLimiterSweepIsBounded(t *testing.T) {
	t.Parallel()

	limiter, now := newTestLimiter(0.001, 1, 1000)
	for i := range 1000 {
		limiter.allow("k" + strconv.Itoa(i))
	}
	keys := make([]string, 0, rateLimitSweepPerRequest+1)
	for e := limiter.order.Front(); len(keys) <= rateLimitSweepPerRequest; e = e.Next() {
		keys = append(keys, entryOf(e).key)
	}
	*now = now.Add(time.Second)
	limiter.allow(keys[0])
	// Exactly rateLimitSweepPerRequest buckets were inspected and rotated.
	if got := entryOf(limiter.order.Front()).key; got != keys[rateLimitSweepPerRequest] {
		t.Fatalf("expected the sweep to advance by %d to %s, front is %s", rateLimitSweepPerRequest, keys[rateLimitSweepPerRequest], got)
	}
	if len(limiter.buckets) != 1000 {
		t.Fatalf("no bucket had refilled, yet the table shrank to %d", len(limiter.buckets))
	}
}

// Overflow use is logged so operators can see saturation, but at most once
// per rateLimitOverflowWarnEvery so a flood cannot flood the log.
func TestIPRateLimiterWarnsOnOverflowAtMostOncePerInterval(t *testing.T) {
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	limiter, now := newTestLimiter(0.001, 100, 1)
	limiter.name = "public"
	limiter.allow("tracked")
	for i := range 5 {
		limiter.allow("rotating-" + strconv.Itoa(i))
	}
	if got := strings.Count(buf.String(), "rate limiter at capacity"); got != 1 {
		t.Fatalf("expected one saturation warning, got %d: %s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "limiter=public") {
		t.Fatalf("warning does not name the limiter: %s", buf.String())
	}
	*now = now.Add(rateLimitOverflowWarnEvery)
	limiter.allow("rotating-later")
	if got := strings.Count(buf.String(), "rate limiter at capacity"); got != 2 {
		t.Fatalf("expected a second warning after the interval, got %d", got)
	}
}
