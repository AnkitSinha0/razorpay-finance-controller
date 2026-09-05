package api

import (
	"math"
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket — no new infra, just an
// in-memory map guarded by a mutex, enough to keep a public write path
// (Phase 16 Tier 2's upload endpoint) from being hit in a loop and
// running up real Vertex AI spend.
type rateLimiter struct {
	mu           sync.Mutex
	buckets      map[string]*tokenBucket
	capacity     float64
	refillPerSec float64
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter builds a limiter allowing capacity requests in a burst,
// refilling at refillPerSec tokens/second thereafter.
func newRateLimiter(capacity, refillPerSec float64) *rateLimiter {
	return &rateLimiter{
		buckets:      make(map[string]*tokenBucket),
		capacity:     capacity,
		refillPerSec: refillPerSec,
	}
}

// Allow reports whether the caller identified by key may proceed right
// now, consuming one token if so.
func (r *rateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.buckets[key]
	now := time.Now()
	if !ok {
		b = &tokenBucket{tokens: r.capacity, last: now}
		r.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens = math.Min(r.capacity, b.tokens+elapsed*r.refillPerSec)
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
