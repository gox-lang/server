package serverruntime

import (
	"strconv"
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func newRateLimitBenchmarkContext(path string, method string) *gox.Context {
	if method == "" {
		method = "GET"
	}
	return &gox.Context{
		Request: gox.NewRequest(gox.RequestInput{
			Method: method,
			IP:     "127.0.0.1",
			URL: gox.NewURL(gox.URLInput{
				String: path,
				Path:   path,
			}),
			Route: gox.NewRoute(gox.RouteInput{
				Matched:  true,
				Original: path,
				Target:   path,
				Pattern:  path,
				Method:   method,
			}),
		}),
		Session: server.NewSessionReference("session-rate-limit-bench"),
	}
}

func newRateLimitFixedWindowLimit() server.RateLimitLimit {
	return server.RateLimitLimit{
		Name:    "default",
		Enabled: true,
		By:      []server.RateLimitFilter{server.RateLimitFilterIP, server.RateLimitFilterRoute},
		Strategy: server.RateLimitFixedWindow{
			Limit:  1000000,
			Window: time.Minute,
		},
	}
}

func TestRateLimitPolicyExactPrefixIndexMatchesManyRules(t *testing.T) {
	const total = 256
	rules := make([]server.RateLimitRule, 0, total)
	for i := 0; i < total; i++ {
		rules = append(rules, server.RateLimitRule{
			Name:    "rule-" + strconv.Itoa(i),
			Enabled: true,
			Methods: []string{"GET"},
			Match:   "/api/exact/" + strconv.Itoa(i),
			Limits:  []server.RateLimitLimit{newRateLimitFixedWindowLimit()},
		})
	}

	policy, err := NewRateLimitPolicy(server.RateLimitConfig{Enabled: true, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}

	if policy.scanStart != total {
		t.Fatalf("scanStart = %d, want %d", policy.scanStart, total)
	}

	matched := policy.Match(newRateLimitBenchmarkContext("/api/exact/255", "GET"))
	if matched == nil || matched.name != "rule-255" {
		t.Fatalf("matched = %#v, want rule-255", matched)
	}
}

func TestRateLimitPolicyExactPrefixPreservesDynamicOrder(t *testing.T) {
	policy, err := NewRateLimitPolicy(server.RateLimitConfig{
		Enabled: true,
		Rules: []server.RateLimitRule{
			{
				Name:    "dynamic-first",
				Enabled: true,
				Methods: []string{"GET"},
				Match:   "/api/:id",
				Limits:  []server.RateLimitLimit{newRateLimitFixedWindowLimit()},
			},
			{
				Name:    "exact-second",
				Enabled: true,
				Methods: []string{"GET"},
				Match:   "/api/123",
				Limits:  []server.RateLimitLimit{newRateLimitFixedWindowLimit()},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if policy.scanStart != 0 {
		t.Fatalf("scanStart = %d, want 0", policy.scanStart)
	}

	matched := policy.Match(newRateLimitBenchmarkContext("/api/123", "GET"))
	if matched == nil || matched.name != "dynamic-first" {
		t.Fatalf("matched = %#v, want dynamic-first", matched)
	}
}

func TestRateLimitPolicyExactPrefixUsesLowestRuleAcrossCandidates(t *testing.T) {
	policy, err := NewRateLimitPolicy(server.RateLimitConfig{
		Enabled: true,
		Rules: []server.RateLimitRule{
			{Name: "pattern-rule", Enabled: true, Methods: []string{"GET"}, Match: "/users/:id", Limits: []server.RateLimitLimit{newRateLimitFixedWindowLimit()}},
			{Name: "original-rule", Enabled: true, Methods: []string{"GET"}, Match: "/users/42", Limits: []server.RateLimitLimit{newRateLimitFixedWindowLimit()}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := newRateLimitBenchmarkContext("/users/42", "GET")
	updateRequestRoute(ctx, gox.NewRoute(gox.RouteInput{
		Matched:  true,
		Original: "/users/42",
		Target:   "/users/show",
		Pattern:  "/users/:id",
		Method:   "GET",
	}))

	matched := policy.Match(ctx)
	if matched == nil || matched.name != "pattern-rule" {
		t.Fatalf("matched = %#v, want pattern-rule", matched)
	}
}
