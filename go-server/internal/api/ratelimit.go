package api

import (
	"container/list"
	"math"
	"sync"
	"time"
)

const (
	// rateLimitMaxBuckets caps the client table so a flood of source
	// addresses cannot grow it without bound.
	rateLimitMaxBuckets = 10000
	// rateLimitSweepPerRequest is how many tracked buckets each request
	// inspects for reclamation. A small constant keeps cleanup O(1) per
	// request while still reclaiming faster than new keys can arrive.
	rateLimitSweepPerRequest = 4
	// rateLimitMaxRetryAfter caps the Retry-After hint.
	rateLimitMaxRetryAfter = 3600
)

// ipRateLimiter is a per-key token bucket with a bounded table.
//
// The HTTP edge keeps this small implementation instead of
// golang.org/x/time/rate. That module is only an indirect dependency, and
// its limiter has no notion of a bounded key table.
//
// Reclamation rules:
//   - A bucket is removed only once it has fully refilled. A full bucket is
//     indistinguishable from a fresh one, so removing it never hands a client
//     quota it has not earned back.
//   - Each request inspects at most rateLimitSweepPerRequest buckets in
//     round-robin order. No request scans the whole table.
//   - When the table is full and no bucket has refilled, unseen keys share a
//     single overflow bucket with the normal rate and burst. Rotating source
//     addresses at capacity therefore cannot exceed that one budget, and
//     tracked clients keep the quota they already spent.
type ipRateLimiter struct {
	rps        float64
	burst      float64
	maxBuckets int
	mu         sync.Mutex
	buckets    map[string]*list.Element // values are *keyedBucket
	order      *list.List               // round-robin sweep order
	overflow   tokenBucket
	now        func() time.Time
}

type tokenBucket struct {
	tokens  float64
	updated time.Time
}

type keyedBucket struct {
	key string
	tokenBucket
}

func newIPRateLimiter(rps float64, burst, maxBuckets int) *ipRateLimiter {
	if maxBuckets <= 0 {
		maxBuckets = rateLimitMaxBuckets
	}
	return &ipRateLimiter{
		rps:        rps,
		burst:      float64(burst),
		maxBuckets: maxBuckets,
		buckets:    make(map[string]*list.Element),
		order:      list.New(),
		overflow:   tokenBucket{tokens: float64(burst)},
		now:        time.Now,
	}
}

// allow reports whether one request may proceed. retryAfter is the
// Retry-After delay in whole seconds when the bucket is empty.
func (l *ipRateLimiter) allow(key string) (bool, int) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now, rateLimitSweepPerRequest)

	var bucket *tokenBucket
	if elem, ok := l.buckets[key]; ok && entryOf(elem) != nil {
		bucket = &entryOf(elem).tokenBucket
	} else if len(l.buckets) < l.maxBuckets {
		kb := &keyedBucket{key: key, tokenBucket: tokenBucket{tokens: l.burst, updated: now}}
		l.buckets[key] = l.order.PushBack(kb)
		bucket = &kb.tokenBucket
	} else {
		bucket = &l.overflow
	}

	l.refill(bucket, now)
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	return false, l.retryAfterSeconds(bucket.tokens)
}

// refill credits the tokens earned since the bucket was last touched.
func (l *ipRateLimiter) refill(b *tokenBucket, now time.Time) {
	if b.updated.IsZero() {
		b.updated = now
		return
	}
	if elapsed := now.Sub(b.updated).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rps)
		b.updated = now
	}
}

// full reports whether b would hold a full burst at now.
func (l *ipRateLimiter) full(b *tokenBucket, now time.Time) bool {
	missing := l.burst - b.tokens
	if missing <= 0 {
		return true
	}
	return now.Sub(b.updated).Seconds()*l.rps >= missing
}

// sweep inspects up to n buckets from the front of the round-robin order.
// Fully refilled buckets are removed; the rest move to the back.
func (l *ipRateLimiter) sweep(now time.Time, n int) {
	for i := 0; i < n && l.order.Len() > 0; i++ {
		elem := l.order.Front()
		kb := entryOf(elem)
		if kb == nil {
			l.order.Remove(elem)
			continue
		}
		if l.full(&kb.tokenBucket, now) {
			l.order.Remove(elem)
			delete(l.buckets, kb.key)
			continue
		}
		l.order.MoveToBack(elem)
	}
}

// entryOf returns the bucket stored in a sweep-order element. Every element
// holds a *keyedBucket; nil is returned only if that invariant breaks.
func entryOf(elem *list.Element) *keyedBucket {
	kb, ok := elem.Value.(*keyedBucket)
	if !ok {
		return nil
	}
	return kb
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
