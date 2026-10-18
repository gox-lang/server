package serverruntime

import "time"

type rateLimitLeakyBucketState struct {
	level     float64
	capacity  float64
	drainRate float64
	lastDrain time.Time
}

func (s *RateLimitStore) AllowLeakyBucket(key string, capacity int, every time.Duration, drain int, now time.Time) RateLimitResult {
	if s == nil || key == "" || capacity <= 0 || every <= 0 || drain <= 0 {
		return RateLimitResult{Allowed: true}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.leakyBuckets == nil {
		s.leakyBuckets = make(map[string]*rateLimitLeakyBucketState)
	}

	s.ticks++

	bucketCapacity := float64(capacity)
	drainRate := float64(drain) / every.Seconds()
	entry := s.leakyBuckets[key]
	if entry == nil {
		entry = &rateLimitLeakyBucketState{capacity: bucketCapacity, drainRate: drainRate, lastDrain: now}
		s.leakyBuckets[key] = entry
	}

	entry.capacity = bucketCapacity
	entry.drainRate = drainRate

	elapsed := now.Sub(entry.lastDrain).Seconds()
	if elapsed > 0 {
		entry.level -= elapsed * entry.drainRate
		if entry.level < 0 {
			entry.level = 0
		}
		entry.lastDrain = now
	}

	if entry.level+1 > entry.capacity {
		missing := entry.level + 1 - entry.capacity
		retryAfter := time.Duration((missing / entry.drainRate) * float64(time.Second))
		if retryAfter < time.Millisecond {
			retryAfter = time.Millisecond
		}
		return RateLimitResult{Allowed: false, Limit: capacity, Remaining: 0, ResetAt: now.Add(retryAfter), RetryAfter: retryAfter}
	}

	entry.level++
	remaining := int(entry.capacity - entry.level)
	if remaining < 0 {
		remaining = 0
	}

	resetAt := now
	if entry.level > 0 {
		resetAt = now.Add(time.Duration((entry.level / entry.drainRate) * float64(time.Second)))
	}

	return RateLimitResult{Allowed: true, Limit: capacity, Remaining: remaining, ResetAt: resetAt}
}
