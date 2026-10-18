package serverruntime

import (
	"net/http"
	"strings"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
)

type MethodPolicy struct {
	allowedMask uint32
	allowHeader string
}

func NewMethodPolicy(methods []string) *MethodPolicy {
	if len(methods) == 0 {
		methods = defaultHTTPMethods()
	}

	var mask uint32
	unique := make([]string, 0, len(methods))
	seen := make(map[string]struct{}, len(methods))

	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			continue
		}

		if _, ok := seen[method]; ok {
			continue
		}

		methodBit := methodMask(method)
		if methodBit == 0 {
			logs.Warm("ignoring unsupported HTTP method from config: %s", method)
			continue
		}

		seen[method] = struct{}{}
		unique = append(unique, method)
		mask |= methodBit
	}

	if mask == 0 {
		logs.Warm("no valid HTTP methods configured; using defaults")
		return NewMethodPolicy(defaultHTTPMethods())
	}

	return &MethodPolicy{
		allowedMask: mask,
		allowHeader: strings.Join(unique, ", "),
	}
}

func (p *MethodPolicy) methodAllowed(method string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return false
	}

	mask := methodMask(method)
	return mask != 0 && p.allowedMask&mask != 0
}

func (r *Runtime) methodsSafe(ctx *gox.Context, req *http.Request) bool {
	policy := r.stateFor(ctx).Methods
	if policy != nil && policy.methodAllowed(ctx.Request.Method()) {
		return true
	}

	if policy != nil && policy.allowHeader != "" {
		ctx.Response.Headers.Set("Allow", policy.allowHeader)
	}

	r.renderHTTPStatusError(ctx, req, nil, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func defaultHTTPMethods() []string {
	return []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodOptions,
	}
}

func methodMask(method string) uint32 {
	switch method {
	case http.MethodGet:
		return methodGET
	case http.MethodPost:
		return methodPOST
	case http.MethodPut:
		return methodPUT
	case http.MethodDelete:
		return methodDELETE
	case http.MethodPatch:
		return methodPATCH
	case http.MethodHead:
		return methodHEAD
	case http.MethodOptions:
		return methodOPTIONS
	default:
		return 0
	}
}
