package serverruntime

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type rateLimitStrategyKind string

const (
	rateLimitStrategyFixedWindow   rateLimitStrategyKind = "fixed_window"
	rateLimitStrategySlidingWindow rateLimitStrategyKind = "sliding_window"
	rateLimitStrategyTokenBucket   rateLimitStrategyKind = "token_bucket"
	rateLimitStrategyLeakyBucket   rateLimitStrategyKind = "leaky_bucket"
)

type RateLimitPolicy struct {
	enabled bool
	rules   []RateLimitRulePolicy

	// exactPrefixRules indexes the leading contiguous block of exact-only rules.
	// Rate-limit rules are ordered, so only this prefix can be safely skipped.
	// Once a dynamic, wildcard, or match-all rule appears, scanning must preserve
	// rule order from that point onward.
	exactPrefixAll      map[string]int
	exactPrefixByMethod map[string]map[string]int
	scanStart           int
}

type rateLimitMatchCandidate struct {
	value      string
	parts      []string
	partCount  int
	partsReady bool
}

type rateLimitMatchCandidateSet struct {
	items [4]rateLimitMatchCandidate
	count int
}

func (s *rateLimitMatchCandidateSet) add(value string) {
	value = normalizePreRoutePath(value)
	if value == "" {
		return
	}

	for i := 0; i < s.count; i++ {
		if s.items[i].value == value {
			return
		}
	}

	if s.count >= len(s.items) {
		return
	}

	s.items[s.count].value = value
	s.count++
}

func (s *rateLimitMatchCandidateSet) parts(index int) ([]string, int) {
	candidate := &s.items[index]
	if !candidate.partsReady {
		candidate.parts = splitPreRouteParts(candidate.value)
		candidate.partCount = len(candidate.parts)
		candidate.partsReady = true
	}

	return candidate.parts, candidate.partCount
}

func buildRateLimitMatchCandidateSet(ctx *gox.Context) rateLimitMatchCandidateSet {
	var candidates rateLimitMatchCandidateSet
	if ctx == nil {
		return candidates
	}

	candidates.add(ctx.Request.Route.Original())
	candidates.add(ctx.Request.URL.Path())
	candidates.add(ctx.Request.Route.Pattern())
	candidates.add(ctx.Request.Route.Target())

	return candidates
}

const (
	rateLimitCleanupTickThreshold uint64 = 1024
	rateLimitCleanupCheckInterval        = 30 * time.Second
	rateLimitIdleTTL                     = 10 * time.Minute
)

var rateLimitPartReplacer = strings.NewReplacer("|", "_", "\n", "_", "\r", "_")

type RateLimitStore struct {
	mu sync.Mutex

	ticks       uint64
	lastCleanup time.Time

	cleanupStop chan struct{}
	closeOnce   sync.Once

	fixedWindows   map[string]*rateLimitFixedWindowState
	tokenBuckets   map[string]*rateLimitTokenBucketState
	slidingWindows map[string]*rateLimitSlidingWindowState
	leakyBuckets   map[string]*rateLimitLeakyBucketState
}

type RateLimitResult struct {
	Allowed    bool
	Limit      int
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

func NewRateLimitStore() *RateLimitStore {
	store := &RateLimitStore{
		lastCleanup:    time.Now(),
		cleanupStop:    make(chan struct{}),
		fixedWindows:   make(map[string]*rateLimitFixedWindowState),
		tokenBuckets:   make(map[string]*rateLimitTokenBucketState),
		slidingWindows: make(map[string]*rateLimitSlidingWindowState),
		leakyBuckets:   make(map[string]*rateLimitLeakyBucketState),
	}

	go store.cleanupLoop()

	return store
}

type RateLimitRulePolicy struct {
	name     string
	enabled  bool
	methods  map[string]struct{}
	all      bool
	match    string
	matchAll bool
	exact    map[string]struct{}
	rules    []preRouteRule
	limits   []RateLimitLimitPolicy
}

type RateLimitLimitPolicy struct {
	name      string
	enabled   bool
	strategy  rateLimitStrategyKind
	by        []server.RateLimitFilter
	keyPrefix string

	limit    int
	window   time.Duration
	every    time.Duration
	refill   int
	burst    int
	drain    int
	capacity int
}

func NewRateLimitPolicy(config server.RateLimitConfig) (*RateLimitPolicy, error) {
	policy := &RateLimitPolicy{enabled: config.Enabled}

	if len(config.Rules) > 0 {
		policy.rules = make([]RateLimitRulePolicy, 0, len(config.Rules))
	}

	for i, ruleConfig := range config.Rules {
		rule, err := newRateLimitRulePolicy(ruleConfig)
		if err != nil {
			return nil, newRuntimeConfigError("compile-ratelimit-rule-failed", fmt.Sprintf("compile rateLimit rule %d failed", i), err)
		}

		policy.rules = append(policy.rules, rule)
	}

	policy.buildExactPrefixIndex()

	if policy.enabled {
		logs.Info("rateLimit loaded: rules=%d", len(policy.rules))
	}

	return policy, nil
}

func newRateLimitRulePolicy(config server.RateLimitRule) (RateLimitRulePolicy, error) {
	match := strings.TrimSpace(config.Match)
	if match == "" {
		match = strings.TrimSpace(config.Pattern)
	}

	matchAll := match == "*"
	if !matchAll {
		match = normalizePreRoutePath(match)
	}

	rule := RateLimitRulePolicy{
		name:     strings.TrimSpace(config.Name),
		enabled:  config.Enabled,
		match:    match,
		matchAll: matchAll,
	}

	if config.Name == "" {
		rule.name = match
	}

	if len(config.Methods) == 0 {
		rule.all = true
	} else {
		rule.methods = make(map[string]struct{}, len(config.Methods))
		for _, method := range config.Methods {
			method = strings.TrimSpace(method)
			if method == "" {
				continue
			}

			if strings.EqualFold(method, server.RateLimitAll) {
				rule.all = true
				continue
			}

			rule.methods[strings.ToLower(method)] = struct{}{}
		}
	}

	if len(rule.methods) == 0 && !rule.all {
		rule.all = true
	}

	if match != "" {
		expanded, err := expandPreRouteChoices(match)
		if err != nil {
			return RateLimitRulePolicy{}, err
		}

		for _, pattern := range expanded {
			pattern = normalizePreRoutePath(pattern)
			if pattern == "" {
				continue
			}

			compiled, err := compilePreRouteRule(pattern, pattern)
			if err != nil {
				return RateLimitRulePolicy{}, err
			}

			if !compiled.hasParam {
				if rule.exact == nil {
					rule.exact = make(map[string]struct{}, len(expanded))
				}
				rule.exact[compiled.from] = struct{}{}
				continue
			}

			rule.rules = append(rule.rules, compiled)
		}
	}

	if len(config.Limits) > 0 {
		rule.limits = make([]RateLimitLimitPolicy, 0, len(config.Limits))
	}

	for i, limitConfig := range config.Limits {
		limit, err := newRateLimitLimitPolicy(limitConfig)
		if err != nil {
			return RateLimitRulePolicy{}, newRuntimeConfigError("compile-ratelimit-limit-failed", fmt.Sprintf("compile rateLimit limit %d failed", i), err)
		}
		rule.limits = append(rule.limits, limit)
	}

	for i := range rule.limits {
		rule.limits[i].keyPrefix = buildRateLimitKeyPrefix(&rule, &rule.limits[i])
	}

	return rule, nil
}

func newRateLimitLimitPolicy(config server.RateLimitLimit) (RateLimitLimitPolicy, error) {
	limit := RateLimitLimitPolicy{
		name:    strings.TrimSpace(config.Name),
		enabled: config.Enabled,
		by:      append([]server.RateLimitFilter(nil), config.By...),
	}

	if len(limit.by) == 0 {
		limit.by = []server.RateLimitFilter{server.RateLimitFilterSession, server.RateLimitFilterIP, server.RateLimitFilterRoute, server.RateLimitFilterMethod}
	}

	switch strategy := config.Strategy.(type) {
	case nil:
		limit.strategy = rateLimitStrategyFixedWindow
	case server.RateLimitFixedWindow:
		limit.strategy = rateLimitStrategyFixedWindow
		limit.limit = strategy.Limit
		limit.window = strategy.Window
	case server.RateLimitSlidingWindow:
		limit.strategy = rateLimitStrategySlidingWindow
		limit.limit = strategy.Limit
		limit.window = strategy.Window
	case server.RateLimitTokenBucket:
		limit.strategy = rateLimitStrategyTokenBucket
		limit.every = strategy.Every
		limit.refill = strategy.Refill
		limit.burst = strategy.Burst
	case server.RateLimitLeakyBucket:
		limit.strategy = rateLimitStrategyLeakyBucket
		limit.every = strategy.Every
		limit.drain = strategy.Drain
		limit.capacity = strategy.Capacity
	default:
		return RateLimitLimitPolicy{}, newRuntimeConfigError("unsupported-ratelimit-strategy", fmt.Sprintf("unsupported rateLimit strategy %T", config.Strategy), nil)
	}

	return limit, nil
}

func (r *Runtime) rateLimit(ctx *gox.Context, req *http.Request) bool {
	_ = req

	policy := r.stateFor(ctx).RateLimit
	if policy == nil || !policy.enabled {
		return true
	}

	rule := policy.Match(ctx)
	if rule == nil || !rule.enabled {
		return true
	}

	store := r.rateLimitStore
	if store == nil {
		return true
	}

	for i := range rule.limits {
		limit := &rule.limits[i]
		if !limit.enabled {
			continue
		}

		result := limit.Allow(ctx, rule, store)
		writeRateLimitHeaders(ctx, result)
		if !result.Allowed {
			r.renderHTTPStatusError(ctx, req, nil, http.StatusTooManyRequests, "rate limit exceeded")
			return false
		}
	}

	return true
}

func (p *RateLimitPolicy) buildExactPrefixIndex() {
	if p == nil || len(p.rules) == 0 {
		return
	}

	p.scanStart = 0

	for i := range p.rules {
		rule := &p.rules[i]
		if !rule.canUseExactPrefixIndex() {
			p.scanStart = i
			return
		}

		for path := range rule.exact {
			if rule.all {
				if p.exactPrefixAll == nil {
					p.exactPrefixAll = make(map[string]int)
				}
				if _, exists := p.exactPrefixAll[path]; !exists {
					p.exactPrefixAll[path] = i
				}
				continue
			}

			for method := range rule.methods {
				if p.exactPrefixByMethod == nil {
					p.exactPrefixByMethod = make(map[string]map[string]int)
				}
				methodIndex := p.exactPrefixByMethod[method]
				if methodIndex == nil {
					methodIndex = make(map[string]int)
					p.exactPrefixByMethod[method] = methodIndex
				}
				if _, exists := methodIndex[path]; !exists {
					methodIndex[path] = i
				}
			}
		}
	}

	p.scanStart = len(p.rules)
}

func (r *RateLimitRulePolicy) canUseExactPrefixIndex() bool {
	return r != nil && !r.matchAll && len(r.exact) > 0 && len(r.rules) == 0
}

func (p *RateLimitPolicy) matchExactPrefix(candidates *rateLimitMatchCandidateSet, method string) *RateLimitRulePolicy {
	if p == nil || candidates == nil || p.scanStart == 0 {
		return nil
	}

	best := -1

	for i := 0; i < candidates.count; i++ {
		candidate := candidates.items[i].value

		if methodIndex := p.exactPrefixByMethod[method]; methodIndex != nil {
			if index, ok := methodIndex[candidate]; ok && (best < 0 || index < best) {
				best = index
			}
		}

		if index, ok := p.exactPrefixAll[candidate]; ok && (best < 0 || index < best) {
			best = index
		}

		if best >= 0 {
			break
		}
	}

	if best < 0 {
		return nil
	}

	return &p.rules[best]
}

func (p *RateLimitPolicy) Match(ctx *gox.Context) *RateLimitRulePolicy {
	if p == nil || ctx == nil {
		return nil
	}

	method := normalizeRateLimitMethod(ctx.Request.Route.Method())
	candidates := buildRateLimitMatchCandidateSet(ctx)
	if rule := p.matchExactPrefix(&candidates, method); rule != nil {
		return rule
	}

	for i := p.scanStart; i < len(p.rules); i++ {
		rule := &p.rules[i]
		if rule.matchesMethodAndCandidates(method, &candidates) {
			return rule
		}
	}

	return nil
}

func (r *RateLimitRulePolicy) matches(ctx *gox.Context) bool {
	if ctx == nil {
		return false
	}
	return r.matchesMethodAndPattern(ctx, normalizeRateLimitMethod(ctx.Request.Route.Method()))
}

func (r *RateLimitRulePolicy) matchesMethodAndPattern(ctx *gox.Context, method string) bool {
	if r == nil || ctx == nil {
		return false
	}

	candidates := buildRateLimitMatchCandidateSet(ctx)
	return r.matchesMethodAndCandidates(method, &candidates)
}

func (r *RateLimitRulePolicy) matchesMethodAndCandidates(method string, candidates *rateLimitMatchCandidateSet) bool {
	if r == nil || candidates == nil {
		return false
	}

	if !r.matchMethodNormalized(method) {
		return false
	}

	return r.matchPatternCandidates(candidates)
}

func (r *RateLimitRulePolicy) matchMethod(method string) bool {
	return r.matchMethodNormalized(normalizeRateLimitMethod(method))
}

func (r *RateLimitRulePolicy) matchMethodNormalized(method string) bool {
	if r.all {
		return true
	}

	_, ok := r.methods[method]
	return ok
}

func normalizeRateLimitMethod(method string) string {
	method = strings.TrimSpace(method)
	switch method {
	case "GET", "get":
		return "get"
	case "POST", "post":
		return "post"
	case "PUT", "put":
		return "put"
	case "PATCH", "patch":
		return "patch"
	case "DELETE", "delete":
		return "delete"
	case "HEAD", "head":
		return "head"
	case "OPTIONS", "options":
		return "options"
	}
	return strings.ToLower(method)
}

func (r *RateLimitRulePolicy) matchPattern(ctx *gox.Context) bool {
	candidates := buildRateLimitMatchCandidateSet(ctx)
	return r.matchPatternCandidates(&candidates)
}

func (r *RateLimitRulePolicy) matchPatternCandidates(candidates *rateLimitMatchCandidateSet) bool {
	if r.match == "" || r.matchAll {
		return true
	}

	if candidates == nil {
		return false
	}

	for candidateIndex := 0; candidateIndex < candidates.count; candidateIndex++ {
		candidate := candidates.items[candidateIndex].value

		if len(r.exact) > 0 {
			if _, ok := r.exact[candidate]; ok {
				return true
			}
		}

		if len(r.rules) == 0 {
			continue
		}

		var parts []string
		partCount := -1
		for i := range r.rules {
			rule := &r.rules[i]
			if candidate == rule.from {
				return true
			}

			if partCount < 0 {
				parts, partCount = candidates.parts(candidateIndex)
			}
			if partCount < rule.minParts {
				continue
			}
			if rule.maxParts >= 0 && partCount > rule.maxParts {
				continue
			}

			if _, ok := rule.matchParts(parts, partCount); ok {
				return true
			}
		}
	}

	return false
}

func (c *RateLimitLimitPolicy) Allow(ctx *gox.Context, rule *RateLimitRulePolicy, store *RateLimitStore) RateLimitResult {
	if c == nil || store == nil {
		return RateLimitResult{Allowed: true}
	}

	key := buildRateLimitKey(ctx, rule, c)
	now := time.Now()

	switch c.strategy {
	case rateLimitStrategyFixedWindow, "":
		return store.AllowFixedWindow(key, c.limit, c.window, now)
	case rateLimitStrategyTokenBucket:
		return store.AllowTokenBucket(key, c.burst, c.every, c.refill, now)
	case rateLimitStrategySlidingWindow:
		return store.AllowSlidingWindow(key, c.limit, c.window, now)
	case rateLimitStrategyLeakyBucket:
		return store.AllowLeakyBucket(key, c.capacity, c.every, c.drain, now)
	default:
		return RateLimitResult{Allowed: true}
	}
}

func (s *RateLimitStore) Close() {
	if s == nil {
		return
	}

	s.closeOnce.Do(func() {
		close(s.cleanupStop)
	})
}

func (s *RateLimitStore) cleanupLoop() {
	ticker := time.NewTicker(rateLimitCleanupCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case now := <-ticker.C:
			s.cleanupIfNeeded(now)
		case <-s.cleanupStop:
			return
		}
	}
}

func (s *RateLimitStore) cleanupIfNeeded(now time.Time) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ticks < rateLimitCleanupTickThreshold && now.Sub(s.lastCleanup) < rateLimitIdleTTL {
		return
	}

	s.cleanupExpiredLocked(now)
	s.ticks = 0
	s.lastCleanup = now
}

func (s *RateLimitStore) cleanupExpiredLocked(now time.Time) {
	for key, entry := range s.fixedWindows {
		if entry == nil || !now.Before(entry.resetAt) {
			delete(s.fixedWindows, key)
		}
	}

	for key, entry := range s.tokenBuckets {
		if entry == nil || now.Sub(entry.lastRefill) > rateLimitIdleTTL {
			delete(s.tokenBuckets, key)
		}
	}

	for key, entries := range s.slidingWindows {
		if entries == nil || len(*entries) == 0 || now.Sub((*entries)[len(*entries)-1]) > rateLimitIdleTTL {
			delete(s.slidingWindows, key)
		}
	}

	for key, entry := range s.leakyBuckets {
		if entry == nil || now.Sub(entry.lastDrain) > rateLimitIdleTTL {
			delete(s.leakyBuckets, key)
		}
	}
}

func buildRateLimitKeyPrefix(rule *RateLimitRulePolicy, limit *RateLimitLimitPolicy) string {
	var b strings.Builder
	b.Grow(64)
	b.WriteString("rl")
	b.WriteString("|rule:")
	b.WriteString(safeRateLimitPart(ruleName(rule)))
	b.WriteString("|limit:")
	b.WriteString(safeRateLimitPart(limitName(limit)))
	b.WriteString("|strategy:")
	if limit != nil {
		b.WriteString(safeRateLimitPart(string(limit.strategy)))
	}
	return b.String()
}

func buildRateLimitKey(ctx *gox.Context, rule *RateLimitRulePolicy, limit *RateLimitLimitPolicy) string {
	var b strings.Builder
	if limit != nil && limit.keyPrefix != "" {
		b.Grow(len(limit.keyPrefix) + 128)
		b.WriteString(limit.keyPrefix)
	} else {
		prefix := buildRateLimitKeyPrefix(rule, limit)
		b.Grow(len(prefix) + 128)
		b.WriteString(prefix)
	}

	for _, filter := range limit.by {
		b.WriteByte('|')
		b.WriteString(string(filter))
		b.WriteByte(':')

		switch filter {
		case server.RateLimitFilterSession:
			b.WriteString(safeRateLimitPart(ctx.Session.ID()))
		case server.RateLimitFilterIP:
			b.WriteString(safeRateLimitPart(ctx.Request.IP()))
		case server.RateLimitFilterRoute:
			b.WriteString(safeRateLimitPart(rateLimitRoute(ctx)))
		case server.RateLimitFilterMethod:
			b.WriteString(safeRateLimitPart(ctx.Request.Route.Method()))
		case server.RateLimitFilterURL:
			b.WriteString(safeRateLimitPart(ctx.Request.Route.Original()))
		default:
			b.WriteString("unknown")
		}
	}

	return b.String()
}

func rateLimitRoute(ctx *gox.Context) string {
	if ctx == nil {
		return ""
	}

	if ctx.Request.Route.Pattern() != "" {
		return ctx.Request.Route.Pattern()
	}

	if ctx.Request.Route.Target() != "" {
		return ctx.Request.Route.Target()
	}

	return ctx.Request.URL.Path()
}

func ruleName(rule *RateLimitRulePolicy) string {
	if rule == nil {
		return "default"
	}

	if rule.name != "" {
		return rule.name
	}

	if rule.match != "" {
		return rule.match
	}

	return "default"
}

func limitName(limit *RateLimitLimitPolicy) string {
	if limit == nil {
		return "default"
	}

	if limit.name != "" {
		return limit.name
	}

	return string(limit.strategy)
}

func safeRateLimitPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}

	return rateLimitPartReplacer.Replace(value)
}

func writeRateLimitHeaders(ctx *gox.Context, result RateLimitResult) {
	if ctx == nil {
		return
	}

	if result.RetryAfter > 0 {
		seconds := int(result.RetryAfter.Seconds())
		if result.RetryAfter%time.Second != 0 {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		ctx.Response.Headers.Set("Retry-After", strconv.Itoa(seconds))
	}

	if result.Limit > 0 {
		ctx.Response.Headers.Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
		ctx.Response.Headers.Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	}

	if !result.ResetAt.IsZero() {
		ctx.Response.Headers.Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
	}
}
