package serverruntime

import (
	"context"
	"github.com/gox-lang/server/internal/toolchain"
	"github.com/gox-lang/gox/server"
	"github.com/libdns/libdns"
	"testing"
)

type configDNSProvider struct{}

func (*configDNSProvider) AppendRecords(context.Context, string, []libdns.Record) ([]libdns.Record, error) {
	panic("validation must not call DNS")
}
func (*configDNSProvider) DeleteRecords(context.Context, string, []libdns.Record) ([]libdns.Record, error) {
	panic("validation must not call DNS")
}

func TestTLSConfigurationModes(t *testing.T) {
	for _, mode := range []string{"", "auto", "http-01", "tls-alpn-01", "dns-01", "manual"} {
		t.Run(mode, func(t *testing.T) {
			cfg := server.TLSConfig{Enabled: true, Type: mode, Domains: []string{"example.com"}}
			if mode == "dns-01" {
				cfg.DNS = &configDNSProvider{}
			}
			if mode == "manual" {
				cfg.Domains = nil
				cfg.CertFile = "cert.pem"
				cfg.KeyFile = "key.pem"
			}
			got, err := normalizeTLSConfig(server.HTTPConfig{TLS: cfg})
			if err != nil {
				t.Fatal(err)
			}
			if got.Port != 443 {
				t.Fatalf("default port=%d", got.Port)
			}
			if mode == "" && got.Type != "auto" {
				t.Fatalf("default type=%s", got.Type)
			}
		})
	}
	cfg := server.TLSConfig{Enabled: true, Type: "auto", Domains: []string{"example.com", "*.example.com"}, DNS: &configDNSProvider{}, Port: 8443}
	if _, err := normalizeTLSConfig(server.HTTPConfig{Port: 8080, TLS: cfg}); err != nil {
		t.Fatal(err)
	}
}
func TestTLSConfigurationRejectsContradictions(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*server.HTTPConfig)
	}{
		{"unknown", func(c *server.HTTPConfig) { c.TLS.Type = "AUTO" }},
		{"negative", func(c *server.HTTPConfig) { c.TLS.Port = -1 }},
		{"large", func(c *server.HTTPConfig) { c.TLS.Port = 65536 }},
		{"collision", func(c *server.HTTPConfig) { c.TLS.Port = 3000 }},
		{"no domains", func(c *server.HTTPConfig) { c.TLS.Domains = nil }},
		{"url", func(c *server.HTTPConfig) { c.TLS.Domains = []string{"https://example.com"} }},
		{"wildcard", func(c *server.HTTPConfig) { c.TLS.Domains = []string{"*.example.com"} }},
		{"invalid wildcard", func(c *server.HTTPConfig) { c.TLS.Domains = []string{"foo.*.com"}; c.TLS.DNS = &configDNSProvider{} }},
		{"no DNS", func(c *server.HTTPConfig) { c.TLS.Type = "dns-01" }},
		{"nil DNS", func(c *server.HTTPConfig) { c.TLS.DNS = (*configDNSProvider)(nil) }},
		{"HTTP DNS", func(c *server.HTTPConfig) { c.TLS.Type = "http-01"; c.TLS.DNS = &configDNSProvider{} }},
		{"ALPN DNS", func(c *server.HTTPConfig) { c.TLS.Type = "tls-alpn-01"; c.TLS.DNS = &configDNSProvider{} }},
		{"auto cert", func(c *server.HTTPConfig) { c.TLS.CertFile = "cert.pem" }},
		{"manual missing key", func(c *server.HTTPConfig) { c.TLS.Type = "manual"; c.TLS.CertFile = "cert.pem" }},
		{"manual DNS", func(c *server.HTTPConfig) {
			c.TLS.Type = "manual"
			c.TLS.CertFile = "cert.pem"
			c.TLS.KeyFile = "key.pem"
			c.TLS.DNS = &configDNSProvider{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := server.HTTPConfig{TLS: server.TLSConfig{Enabled: true, Domains: []string{"example.com"}}}
			tc.edit(&cfg)
			if _, err := normalizeTLSConfig(cfg); err == nil {
				t.Fatal("expected error")
			}
			cfg.TLS.Enabled = false
			if _, err := normalizeTLSConfig(cfg); err != nil {
				t.Fatalf("disabled TLS: %v", err)
			}
		})
	}
}
func TestTLSInvalidFailsBeforeRuntimeResources(t *testing.T) {
	_, err := New(server.Config{HTTP: server.HTTPConfig{TLS: server.TLSConfig{Enabled: true, Type: "bad"}}}, nil, nil, toolchain.Paths{}, nil)
	if err == nil {
		t.Fatal("expected validation error before accessing project")
	}
}

func TestTLSInvalidRejectedWhenPreparingReloadState(t *testing.T) {
	_, err := NewRuntimeState(server.Config{HTTP: server.HTTPConfig{TLS: server.TLSConfig{Enabled: true, Type: "dns-01", Domains: []string{"example.com"}}}}, t.TempDir(), nil)
	if err == nil {
		t.Fatal("invalid TLS configuration accepted by reload state")
	}
}
