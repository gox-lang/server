package serverruntime

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/gox-lang/gox/server"
)

// normalizeTLSConfig validates configuration without network, filesystem writes,
// or certificate issuance. Disabled TLS leaves HTTP behavior unchanged.
func normalizeTLSConfig(httpConfig server.HTTPConfig) (server.TLSConfig, error) {
	cfg := httpConfig.TLS
	if !cfg.Enabled {
		return cfg, nil
	}
	fail := func(message string) (server.TLSConfig, error) {
		return cfg, newRuntimeConfigError("invalid-tls-config", message, nil)
	}
	if cfg.Type == "" {
		cfg.Type = "auto"
	}
	switch cfg.Type {
	case "auto", "http-01", "tls-alpn-01", "dns-01", "manual":
	default:
		return fail("TLS.Type must be auto, http-01, tls-alpn-01, dns-01, or manual")
	}
	if cfg.Port == 0 {
		cfg.Port = 443
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fail("TLS.Port must be between 1 and 65535")
	}
	httpPort := httpConfig.Port
	if httpPort == 0 {
		httpPort = 3000
	}
	if httpPort < 1 || httpPort > 65535 {
		return fail("HTTP.Port must be between 1 and 65535")
	}
	if cfg.Port == httpPort {
		return fail("HTTP.Port and TLS.Port must be different")
	}
	hasDNS := cfg.DNS != nil
	if hasDNS {
		value := reflect.ValueOf(cfg.DNS)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if value.IsNil() {
				return fail("TLS.DNS must not contain a nil provider")
			}
		}
	}
	if cfg.Type == "manual" {
		if strings.TrimSpace(cfg.CertFile) == "" || strings.TrimSpace(cfg.KeyFile) == "" {
			return fail("manual TLS requires CertFile and KeyFile")
		}
		if hasDNS || cfg.Email != "" {
			return fail("manual TLS does not accept DNS or an ACME Email")
		}
		return cfg, nil
	}
	if cfg.CertFile != "" || cfg.KeyFile != "" {
		return fail("automatic TLS does not accept CertFile or KeyFile")
	}
	if cfg.Type == "dns-01" && !hasDNS {
		return fail("TLS.Type dns-01 requires DNS")
	}
	if hasDNS && (cfg.Type == "http-01" || cfg.Type == "tls-alpn-01") {
		return fail("TLS.DNS cannot be combined with http-01 or tls-alpn-01")
	}
	if len(cfg.Domains) == 0 {
		return fail("automatic TLS requires at least one domain")
	}
	for _, domain := range cfg.Domains {
		if domain == "" || strings.ContainsAny(domain, " \t\r\n/:?#@\\") {
			return fail(fmt.Sprintf("invalid TLS domain %q: use a hostname without a scheme, path, or port", domain))
		}
		if strings.Contains(domain, "*") {
			if !strings.HasPrefix(domain, "*.") || len(domain) <= 2 || strings.Contains(domain[2:], "*") {
				return fail(fmt.Sprintf("invalid wildcard TLS domain %q", domain))
			}
			if !hasDNS {
				return fail("wildcard TLS domains require DNS-01 and a DNS provider")
			}
		}
	}
	return cfg, nil
}
