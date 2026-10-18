package serverruntime

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type preRouteSegmentKind uint8

const (
	preRouteLiteral preRouteSegmentKind = iota
	preRouteParam
	preRouteWildcard
)

type preRouteSegment struct {
	kind     preRouteSegmentKind
	value    string
	prefix   string
	suffix   string
	optional bool
}

type preRouteRule struct {
	from     string
	target   string
	segments []preRouteSegment
	minParts int
	maxParts int
	hasParam bool
	wildcard bool
}

type PreRouter struct {
	exact map[string]string
	rules []preRouteRule
}

func NewPreRouter(config server.RoutesConfig) (*PreRouter, error) {
	router := &PreRouter{}

	if len(config.Routes) == 0 {
		return router, nil
	}

	exact := make(map[string]string, len(config.Routes))

	for from, target := range config.Routes {
		from = normalizePreRoutePath(from)
		target = normalizePreRouteTarget(target)

		if from == "" || target == "" {
			continue
		}

		expanded, err := expandPreRouteChoices(from)
		if err != nil {
			return nil, err
		}

		for _, pattern := range expanded {
			rule, err := compilePreRouteRule(pattern, target)
			if err != nil {
				return nil, err
			}

			if !rule.hasParam {
				exact[rule.from] = rule.target
				continue
			}

			router.rules = append(router.rules, rule)
		}
	}

	if len(exact) > 0 {
		router.exact = exact
	}

	return router, nil
}

func (r *Runtime) preRoute(ctx *gox.Context) {
	original := ctx.Request.URL.Path()
	target := normalizePreRouteTarget(original)
	if target == "" {
		target = "/index"
	}

	updateRequestRoute(ctx, gox.NewRoute(gox.RouteInput{
		Matched:  false,
		Original: original,
		Target:   target,
		Pattern:  target,
		Method:   resolveGoxRouteMethod(ctx),
		IsStatic: filepath.Ext(target) != "",
		IsAction: isGoxActionRequest(ctx),
		Params:   gox.NewCollection[string](nil),
	}))

	preRouter := r.stateFor(ctx).PreRouter
	if preRouter == nil {
		return
	}

	// matchedTarget, params, ok := preRouter.Match(original)
	matchedPattern, matchedTarget, params, ok := preRouter.Match(original)
	if !ok {
		return
	}

	updateRequestRoute(ctx, gox.NewRoute(gox.RouteInput{
		Matched:  true,
		Original: original,
		Target:   matchedTarget,
		Pattern:  matchedPattern,
		Method:   resolveGoxRouteMethod(ctx),
		IsStatic: filepath.Ext(matchedTarget) != "",
		IsAction: isGoxActionRequest(ctx),
		Params:   gox.NewCollection[string](singleValueMap(params)),
	}))
}

func (p *PreRouter) LogLoaded() {
	if p == nil {
		return
	}

	exactCount := 0
	if p.exact != nil {
		exactCount = len(p.exact)
	}

	logs.Info("preRoute loaded: exact=%d dynamic=%d", exactCount, len(p.rules))

	if exactCount > 0 {
		keys := make([]string, 0, exactCount)
		for from := range p.exact {
			keys = append(keys, from)
		}
		sort.Strings(keys)

		for _, from := range keys {
			logs.Info("preRoute exact: %s -> %s", from, p.exact[from])
		}
	}

	for i := range p.rules {
		rule := &p.rules[i]
		logs.Info("preRoute dynamic: %s -> %s", rule.from, rule.target)
	}
}

func (p *PreRouter) Match(path string) (pattern string, target string, params map[string]string, ok bool) {
	path = normalizePreRoutePath(path)
	if path == "" {
		return "", "", nil, false
	}

	if p.exact != nil {
		if target, ok := p.exact[path]; ok {
			return path, target, nil, true
		}
	}

	parts := splitPreRouteParts(path)
	partCount := len(parts)

	for i := range p.rules {
		rule := &p.rules[i]
		if partCount < rule.minParts {
			continue
		}
		if rule.maxParts >= 0 && partCount > rule.maxParts {
			continue
		}

		params, ok := rule.matchParts(parts, partCount)
		if ok {
			return rule.from, rule.target, params, true
		}
	}

	return "", "", nil, false
}

func (r *preRouteRule) match(path string, partCount int) (map[string]string, bool) {
	return r.matchParts(splitPreRouteParts(path), partCount)
}

func (r *preRouteRule) matchParts(segments []string, partCount int) (map[string]string, bool) {
	segmentIndex := 0
	var params map[string]string

	for patternIndex := 0; patternIndex < len(r.segments); patternIndex++ {
		expected := r.segments[patternIndex]

		if expected.kind == preRouteWildcard {
			if patternIndex != len(r.segments)-1 {
				return nil, false
			}

			// A pure final wildcard matches zero or more trailing segments.
			// Examples:
			//   *         => /, /index, /images/logo.png
			//   /images/* => /images, /images/logo.png, /images/a/b.png
			if segmentIndex >= len(segments) {
				if expected.prefix == "" && expected.suffix == "" {
					return params, true
				}
				return nil, false
			}

			remainder := strings.Join(segments[segmentIndex:], "/")
			if remainder == "" {
				return nil, false
			}
			if expected.prefix != "" && !strings.HasPrefix(remainder, expected.prefix) {
				return nil, false
			}
			if expected.suffix != "" && !strings.HasSuffix(remainder, expected.suffix) {
				return nil, false
			}

			return params, true
		}

		if segmentIndex >= len(segments) {
			if expected.optional {
				continue
			}
			return nil, false
		}

		segment := segments[segmentIndex]

		switch expected.kind {
		case preRouteLiteral:
			if segment != expected.value {
				return nil, false
			}

		case preRouteParam:
			if segment == "" {
				return nil, false
			}
			if expected.prefix != "" && !strings.HasPrefix(segment, expected.prefix) {
				return nil, false
			}
			if expected.suffix != "" && !strings.HasSuffix(segment, expected.suffix) {
				return nil, false
			}

			if params == nil {
				params = make(map[string]string, partCount)
			}
			params[expected.value] = segment
		}

		segmentIndex++
	}

	if segmentIndex != len(segments) {
		return nil, false
	}

	return params, true
}

func compilePreRouteRule(pattern string, target string) (preRouteRule, error) {
	parts := splitPreRouteParts(pattern)

	rule := preRouteRule{
		from:     pattern,
		target:   target,
		segments: make([]preRouteSegment, 0, len(parts)),
		maxParts: len(parts),
	}

	optionalStarted := false

	for i, part := range parts {
		if part == "" {
			continue
		}

		segment := preRouteSegment{}

		if strings.Contains(part, "*") {
			if i != len(parts)-1 {
				return preRouteRule{}, newPreRouteErrorf("route-wildcard-not-final", pattern, "route wildcard is only valid in the final segment")
			}
			if strings.Count(part, "*") != 1 {
				return preRouteRule{}, newPreRouteErrorf("route-wildcard-repeated", pattern, "route wildcard can be used only once")
			}
			if optionalStarted {
				return preRouteRule{}, newPreRouteErrorf("route-wildcard-after-optional", pattern, "wildcard route segment cannot appear after an optional route segment")
			}

			pieces := strings.SplitN(part, "*", 2)
			segment.kind = preRouteWildcard
			segment.prefix = pieces[0]
			segment.suffix = pieces[1]
			rule.hasParam = true
			rule.wildcard = true
			rule.maxParts = -1

			// Pure final wildcards match zero or more trailing segments, so they
			// do not increase minParts. Partial wildcards still require a real
			// segment to test against.
			if segment.prefix != "" || segment.suffix != "" {
				rule.minParts++
			}

			rule.segments = append(rule.segments, segment)
			continue
		}

		if strings.HasPrefix(part, ":") {
			name, suffix, optional, err := parsePreRouteParamSegment(part)
			if err != nil {
				return preRouteRule{}, wrapPreRouteError("invalid-route-param", pattern, err)
			}

			segment.kind = preRouteParam
			segment.value = name
			segment.suffix = suffix
			segment.optional = optional
			rule.hasParam = true

			if optional {
				optionalStarted = true
			} else if optionalStarted {
				return preRouteRule{}, newPreRouteErrorf("route-required-after-optional", pattern, "required route segment after optional segment")
			}
		} else {
			if optionalStarted {
				return preRouteRule{}, newPreRouteErrorf("route-literal-after-optional", pattern, "literal route segment after optional segment")
			}

			segment.kind = preRouteLiteral
			segment.value = part
		}

		if !segment.optional {
			rule.minParts++
		}

		rule.segments = append(rule.segments, segment)
	}

	return rule, nil
}

func parsePreRouteParamSegment(part string) (string, string, bool, error) {
	body := strings.TrimPrefix(part, ":")
	if body == "" || body == "?" {
		return "", "", false, newPreRouteErrorf("invalid-route-param", "", "invalid route param")
	}

	optional := false
	if strings.HasPrefix(body, "?") {
		optional = true
		body = strings.TrimPrefix(body, "?")
	} else if strings.HasSuffix(body, "?") {
		// Backward compatibility for the old :param? syntax when the
		// optional marker is the final character of the segment.
		optional = true
		body = strings.TrimSuffix(body, "?")
	}
	if body == "" {
		return "", "", false, newPreRouteErrorf("invalid-optional-route-param", "", "invalid optional route param")
	}

	nameEnd := len(body)
	for i, r := range body {
		if !isPreRouteParamNameRune(r) {
			nameEnd = i
			break
		}
	}

	name := body[:nameEnd]
	suffix := body[nameEnd:]
	if name == "" {
		return "", "", false, newPreRouteErrorf("invalid-route-param", "", "invalid route param")
	}

	return name, suffix, optional, nil
}

func isPreRouteParamNameRune(r rune) bool {
	return r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func expandPreRouteChoices(pattern string) ([]string, error) {
	parts := splitPreRouteParts(pattern)
	if len(parts) == 0 {
		return []string{"/"}, nil
	}

	results := []string{""}

	for _, part := range parts {
		if part == "" {
			continue
		}

		choices, err := expandPreRouteSegmentChoices(part)
		if err != nil {
			return nil, err
		}

		next := make([]string, 0, len(results)*len(choices))
		for _, prefix := range results {
			for _, choice := range choices {
				next = append(next, prefix+"/"+choice)
			}
		}

		results = next
	}

	if len(results) == 0 {
		return []string{"/"}, nil
	}

	return results, nil
}

func expandPreRouteSegmentChoices(part string) ([]string, error) {
	open := strings.IndexByte(part, '(')
	if open < 0 {
		if strings.Contains(part, ")") {
			return nil, newPreRouteErrorf("invalid-route-choice-segment", part, "invalid route choice segment: %s", part)
		}
		return []string{part}, nil
	}

	close := strings.IndexByte(part[open+1:], ')')
	if close < 0 {
		return nil, newPreRouteErrorf("invalid-route-choice-segment", part, "invalid route choice segment: %s", part)
	}
	close += open + 1

	if strings.Contains(part[open+1:close], "(") {
		return nil, newPreRouteErrorf("invalid-nested-route-choice-segment", part, "invalid nested route choice segment: %s", part)
	}

	body := part[open+1 : close]
	if body == "" {
		return nil, newPreRouteErrorf("empty-route-choice-segment", "", "empty route choice segment")
	}

	choices := strings.Split(body, "|")
	for _, choice := range choices {
		if choice == "" {
			return nil, newPreRouteErrorf("empty-route-choice-value", part, "empty route choice value in %s", part)
		}
	}

	prefix := part[:open]
	suffix := part[close+1:]

	expandedSuffix, err := expandPreRouteSegmentChoices(suffix)
	if err != nil {
		return nil, err
	}

	results := make([]string, 0, len(choices)*len(expandedSuffix))
	for _, choice := range choices {
		for _, rest := range expandedSuffix {
			results = append(results, prefix+choice+rest)
		}
	}

	return results, nil
}

func splitPreRouteParts(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}

	return strings.Split(path, "/")
}

func countPreRouteParts(path string) int {
	path = strings.Trim(path, "/")
	if path == "" {
		return 0
	}

	count := 1
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			count++
		}
	}

	return count
}

func normalizePreRoutePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}

	return path
}

func normalizePreRouteTarget(path string) string {
	path = normalizePreRoutePath(path)
	if path == "/" {
		return "/index"
	}

	return path
}

func resolveGoxRouteMethod(ctx *gox.Context) string {
	if ctx == nil {
		return ""
	}

	if isGoxActionRequest(ctx) {
		method, _ := ctx.Request.Headers.Get("Invoke-Action")
		return strings.TrimSpace(method)
	}

	return normalizeHTTPMethodToGox(ctx.Request.Method())
}

func isGoxActionRequest(ctx *gox.Context) bool {
	if ctx == nil {
		return false
	}

	if normalizeHTTPMethodToGox(ctx.Request.Method()) != http.MethodPost {
		return false
	}

	action, ok := ctx.Request.Headers.Get("Invoke-Action")
	return ok && strings.TrimSpace(action) != ""
}

func normalizeHTTPMethodToGox(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return ""
	}

	return strings.ToUpper(method)
}

func isHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT":
		return true
	default:
		return false
	}
}

func singleValueMap(values map[string]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[key] = []string{value}
	}
	return out
}
