package api

import (
	"math"
	"sync"
	"time"
)

const (
	// rateLimitIdleTTL is how long an unused client bucket is kept.
	rateLimitIdleTTL = 10 * time.Minute
	// rateLimitMaxBuckets caps the client table so a flood of source
	// addresses cannot grow it without bound.
	rateLimitMaxBuckets = 10000
	// rateLimitMaxRetryAfter caps the Retry-After hint.
	rateLimitMaxRetryAfter = 3600
)

// ipRateLimiter is a per-key token bucket.
//
// The HTTP edge keeps this small implementation instead of
// golang.org/x/time/rate. That module is only an indirect dependency, and
// its limiter has no idle eviction. A private bucket map lets us drop cold
// clients and cap the table without a new module.
type ipRateLimiter struct {
	rps        float64
	burst      float64
	idleTTL    time.Duration
	maxBuckets int
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	lastSweep  time.Time
	now        func() time.Time
}

type tokenBucket struct {
	tokens  float64
	updated time.Time
}

func newIPRateLimiter(rps float64, burst int, idleTTL time.Duration, maxBuckets int) *ipRateLimiter {
	if idleTTL <= 0 {
		idleTTL = rateLimitIdleTTL
	}
	if maxBuckets <= 0 {
		maxBuckets = rateLimitMaxBuckets
	}
	return &ipRateLimiter{
		rps:        rps,
		burst:      float64(burst),
		idleTTL:    idleTTL,
		maxBuckets: maxBuckets,
		buckets:    make(map[string]*tokenBucket),
		now:        time.Now,
	}
}

// allow reports whether one request may proceed. retryAfter is the
// Retry-After delay in whole seconds when the bucket is empty.
func (l *ipRateLimiter) allow(key string) (bool, int) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastSweep.IsZero() || now.Sub(l.lastSweep) >= time.Second || len(l.buckets) >= l.maxBuckets {
		l.evictIdle(now)
		l.lastSweep = now
	}

	bucket, ok := l.buckets[key]
	if !ok {
		for len(l.buckets) >= l.maxBuckets {
			l.evictOldest()
		}
		bucket = &tokenBucket{tokens: l.burst, updated: now}
		l.buckets[key] = bucket
	} else if elapsed := now.Sub(bucket.updated).Seconds(); elapsed > 0 {
		bucket.tokens += elapsed * l.rps
		if bucket.tokens > l.burst {
			bucket.tokens = l.burst
		}
		bucket.updated = now
	}

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	return false, l.retryAfterSeconds(bucket.tokens)
}

func (l *ipRateLimiter) retryAfterSeconds(tokens float64) int {
	if l.rps <= 0 {
		return 1
	}
	seconds := math.Ceil((1 - tokens) / l.rps)
	if seconds < 1 {
		seconds = 1
	}
	if seconds > rateLimitMaxRetryAfter {
		seconds = rateLimitMaxRetryAfter
	}
	return int(seconds)
}

func (l *ipRateLimiter) evictIdle(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.updated) >= l.idleTTL {
			delete(l.buckets, key)
		}
	}
}

func (l *ipRateLimiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	first := true
	for key, bucket := range l.buckets {
		if first || bucket.updated.Before(oldest) {
			first = false
			oldestKey = key
			oldest = bucket.updated
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}
