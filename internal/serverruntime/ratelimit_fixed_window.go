package serverruntime

import "time"

type rateLimitFixedWindowState struct {
	count   int
	resetAt time.Time
}

func (s *RateLimitStore) AllowFixedWindow(key string, limit int, window time.Duration, now time.Time) RateLimitResult {
	if s == nil || key == "" || limit <= 0 || window <= 0 {
		return RateLimitResult{Allowed: true}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fixedWindows == nil {
		s.fixedWindows = make(map[string]*rateLimitFixedWindowState)
	}

	s.ticks++

	entry := s.fixedWindows[key]
	if entry == nil || !now.Before(entry.resetAt) {
		resetAt := now.Add(window)
		s.fixedWindows[key] = &rateLimitFixedWindowState{count: 1, resetAt: resetAt}
		return RateLimitResult{Allowed: true, Limit: limit, Remaining: limit - 1, ResetAt: resetAt}
	}

	if entry.count >= limit {
		retryAfter := entry.resetAt.Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return RateLimitResult{Allowed: false, Limit: limit, Remaining: 0, ResetAt: entry.resetAt, RetryAfter: retryAfter}
	}

	entry.count++
	remaining := limit - entry.count
	if remaining < 0 {
		remaining = 0
	}

	return RateLimitResult{Allowed: true, Limit: limit, Remaining: remaining, ResetAt: entry.resetAt}
}
