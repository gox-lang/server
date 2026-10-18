package serverruntime

import "time"

type rateLimitTokenBucketState struct {
	tokens     float64
	capacity   float64
	refillRate float64
	lastRefill time.Time
}

func (s *RateLimitStore) AllowTokenBucket(key string, burst int, every time.Duration, refill int, now time.Time) RateLimitResult {
	if s == nil || key == "" || burst <= 0 || every <= 0 || refill <= 0 {
		return RateLimitResult{Allowed: true}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tokenBuckets == nil {
		s.tokenBuckets = make(map[string]*rateLimitTokenBucketState)
	}

	s.ticks++

	capacity := float64(burst)
	refillRate := float64(refill) / every.Seconds()
	entry := s.tokenBuckets[key]
	if entry == nil {
		entry = &rateLimitTokenBucketState{tokens: capacity, capacity: capacity, refillRate: refillRate, lastRefill: now}
		s.tokenBuckets[key] = entry
	}

	entry.capacity = capacity
	entry.refillRate = refillRate

	elapsed := now.Sub(entry.lastRefill).Seconds()
	if elapsed > 0 {
		entry.tokens += elapsed * entry.refillRate
		if entry.tokens > entry.capacity {
			entry.tokens = entry.capacity
		}
		entry.lastRefill = now
	}

	if entry.tokens < 1 {
		missing := 1 - entry.tokens
		retryAfter := time.Duration((missing / entry.refillRate) * float64(time.Second))
		if retryAfter < time.Millisecond {
			retryAfter = time.Millisecond
		}
		return RateLimitResult{Allowed: false, Limit: burst, Remaining: 0, ResetAt: now.Add(retryAfter), RetryAfter: retryAfter}
	}

	entry.tokens--
	remaining := int(entry.tokens)
	if remaining < 0 {
		remaining = 0
	}

	resetAt := now
	if entry.tokens < entry.capacity {
		resetAt = now.Add(time.Duration(((entry.capacity - entry.tokens) / entry.refillRate) * float64(time.Second)))
	}

	return RateLimitResult{Allowed: true, Limit: burst, Remaining: remaining, ResetAt: resetAt}
}
