package api

import (
	"testing"
	"time"
)

func TestIPRateLimiterEvictsIdleBuckets(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newIPRateLimiter(10, 1, 5*time.Second, 10)
	limiter.now = func() time.Time { return now }

	if ok, _ := limiter.allow("old"); !ok {
		t.Fatal("expected the first client to be allowed")
	}
	now = now.Add(6 * time.Second)
	if ok, _ := limiter.allow("new"); !ok {
		t.Fatal("expected a later client to be allowed")
	}
	if _, ok := limiter.buckets["old"]; ok {
		t.Fatal("expected the idle bucket to be evicted")
	}
	if _, ok := limiter.buckets["new"]; !ok {
		t.Fatal("expected the active bucket to remain")
	}
}

func TestIPRateLimiterCapsBucketCount(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	limiter := newIPRateLimiter(10, 2, time.Hour, 2)
	limiter.now = func() time.Time { return now }

	for _, key := range []string{"a", "b", "c"} {
		if ok, _ := limiter.allow(key); !ok {
			t.Fatalf("expected %s to be allowed", key)
		}
		now = now.Add(time.Millisecond)
	}
	if _, ok := limiter.buckets["a"]; ok {
		t.Fatal("expected the oldest bucket to be evicted at the cap")
	}
	if len(limiter.buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(limiter.buckets))
	}
}
