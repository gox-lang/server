package serverruntime

import "time"

type rateLimitSlidingWindowState []time.Time

func (s *RateLimitStore) AllowSlidingWindow(key string, limit int, window time.Duration, now time.Time) RateLimitResult {
	if s == nil || key == "" || limit <= 0 || window <= 0 {
		return RateLimitResult{Allowed: true}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.slidingWindows == nil {
		s.slidingWindows = make(map[string]*rateLimitSlidingWindowState)
	}

	s.ticks++

	cutoff := now.Add(-window)
	entries := s.slidingWindows[key]
	if entries == nil {
		state := rateLimitSlidingWindowState{}
		entries = &state
		s.slidingWindows[key] = entries
	}

	kept := (*entries)[:0]
	for _, ts := range *entries {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	*entries = kept

	if len(*entries) >= limit {
		oldest := (*entries)[0]
		resetAt := oldest.Add(window)
		retryAfter := resetAt.Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return RateLimitResult{Allowed: false, Limit: limit, Remaining: 0, ResetAt: resetAt, RetryAfter: retryAfter}
	}

	*entries = append(*entries, now)
	remaining := limit - len(*entries)
	if remaining < 0 {
		remaining = 0
	}

	resetAt := now.Add(window)
	if len(*entries) > 0 {
		resetAt = (*entries)[0].Add(window)
	}

	return RateLimitResult{Allowed: true, Limit: limit, Remaining: remaining, ResetAt: resetAt}
}
