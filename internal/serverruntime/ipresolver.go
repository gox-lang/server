package serverruntime

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type IPResolverPolicy struct {
	trustedProxies      []netip.Prefix
	headers             []string
	requireTrustedProxy bool
}

func NewIPResolverPolicy(config server.ResolverIPForwardedConfig) (*IPResolverPolicy, error) {
	headers := normalizeForwardedHeaders(config.Headers)

	policy := &IPResolverPolicy{
		trustedProxies:      make([]netip.Prefix, 0, len(config.TrustedProxies)),
		headers:             headers,
		requireTrustedProxy: config.RequireTrustedProxy,
	}

	for _, value := range config.TrustedProxies {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		prefix, err := parseTrustedProxy(value)
		if err != nil {
			return nil, err
		}

		policy.trustedProxies = append(policy.trustedProxies, prefix)
	}

	logs.Info(
		"resolverIPForwarded loaded: trustedProxies=%d headers=%d strict=%v",
		len(policy.trustedProxies),
		len(policy.headers),
		policy.requireTrustedProxy,
	)

	return policy, nil
}

func NewIPResolverPolicyUnsafe(headers []string) *IPResolverPolicy {
	return &IPResolverPolicy{
		headers: normalizeForwardedHeaders(headers),
	}
}

func (r *Runtime) resolveForwardedIP(ctx *gox.Context, req *http.Request) bool {
	policy := r.stateFor(ctx).IPResolver
	if policy == nil || (len(policy.trustedProxies) == 0 && !policy.requireTrustedProxy) {
		return true
	}

	ip, ok := policy.Resolve(req)
	if !ok {
		r.renderHTTPStatusError(ctx, req, nil, http.StatusForbidden, "forbidden")
		return false
	}

	updateRequestIP(ctx, ip)
	return true
}

func (p *IPResolverPolicy) Resolve(req *http.Request) (string, bool) {
	remoteIP := remoteAddrIP(req.RemoteAddr)

	if remoteIP == "" {
		if p.requireTrustedProxy {
			return "", false
		}

		return "", true
	}

	if len(p.trustedProxies) == 0 {
		if p.requireTrustedProxy {
			return "", false
		}

		return remoteIP, true
	}

	remoteAddr, err := netip.ParseAddr(remoteIP)
	if err != nil {
		if p.requireTrustedProxy {
			return "", false
		}

		return remoteIP, true
	}

	remoteAddr = normalizeAddr(remoteAddr)
	if !p.isTrustedProxy(remoteAddr) {
		if p.requireTrustedProxy {
			return "", false
		}

		return remoteAddr.String(), true
	}

	if forwardedIP := p.forwardedIP(req); forwardedIP != "" {
		return forwardedIP, true
	}

	return remoteAddr.String(), true
}

func (p *IPResolverPolicy) isTrustedProxy(addr netip.Addr) bool {
	for _, prefix := range p.trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

func (p *IPResolverPolicy) forwardedIP(req *http.Request) string {
	for _, header := range p.headers {
		value := req.Header.Get(header)
		if value == "" {
			continue
		}

		if ip := firstValidForwardedIP(value); ip != "" {
			return ip
		}
	}

	return ""
}

func firstValidForwardedIP(value string) string {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if host, _, err := net.SplitHostPort(part); err == nil {
			part = host
		}

		addr, err := netip.ParseAddr(part)
		if err != nil {
			continue
		}

		return normalizeAddr(addr).String()
	}

	return ""
}

func remoteAddrIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if remoteAddr[0] == '[' {
		if end := strings.IndexByte(remoteAddr, ']'); end > 0 {
			return remoteAddr[1:end]
		}
	}
	lastColon := strings.LastIndexByte(remoteAddr, ':')
	if lastColon > 0 && strings.IndexByte(remoteAddr, ':') == lastColon {
		return remoteAddr[:lastColon]
	}
	return remoteAddr
}

func parseTrustedProxy(value string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return normalizePrefix(prefix), nil
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}

	addr = normalizeAddr(addr)
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func normalizePrefix(prefix netip.Prefix) netip.Prefix {
	addr := normalizeAddr(prefix.Addr())
	bits := prefix.Bits()

	if addr.Is4() && bits > 32 {
		bits = 32
	}

	return netip.PrefixFrom(addr, bits)
}

func normalizeAddr(addr netip.Addr) netip.Addr {
	if addr.Is4In6() {
		return addr.Unmap()
	}

	return addr
}

func normalizeForwardedHeaders(headers []string) []string {
	if len(headers) == 0 {
		return []string{
			"CF-Connecting-IP",
			"X-Real-IP",
			"X-Forwarded-For",
		}
	}

	out := make([]string, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))

	for _, header := range headers {
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}

		key := strings.ToLower(header)
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, header)
	}

	if len(out) == 0 {
		return []string{
			"CF-Connecting-IP",
			"X-Real-IP",
			"X-Forwarded-For",
		}
	}

	return out
}

// trustsRequest checks the immediate network peer, never a forwarded address.
func (p *IPResolverPolicy) trustsRequest(req *http.Request) bool {
	if p == nil {
		return false
	}
	addr, err := netip.ParseAddr(remoteAddrIP(req.RemoteAddr))
	return err == nil && p.isTrustedProxy(normalizeAddr(addr))
}
