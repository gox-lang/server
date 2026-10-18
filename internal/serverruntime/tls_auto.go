package serverruntime

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/caddyserver/certmagic"
	"github.com/gox-lang/server/internal/toolchain"
	"github.com/gox-lang/gox/server"
)

// Each listener group owns its cache and cancellation. CertMagic's global
// defaults are never modified, so separate runtimes cannot replace each other.
type automaticTLS struct {
	config    *certmagic.Config
	issuer    *certmagic.ACMEIssuer
	cache     *certmagic.Cache
	domains   []string
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func newAutomaticTLS(cfg server.HTTPConfig) (*automaticTLS, error) {
	home, err := toolchain.ResolveGoxHome()
	if err != nil {
		return nil, fmt.Errorf("resolve TLS storage: %w", err)
	}
	storagePath := filepath.Join(home, ".tls")
	if err := os.MkdirAll(storagePath, 0700); err != nil {
		return nil, fmt.Errorf("create TLS storage: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &automaticTLS{ctx: ctx, cancel: cancel, domains: append([]string(nil), cfg.TLS.Domains...)}
	a.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return a.config, nil },
	})
	a.config = certmagic.New(a.cache, certmagic.Config{Storage: &certmagic.FileStorage{Path: storagePath}})
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 3000
	}
	issuer := certmagic.ACMEIssuer{
		CA:    certmagic.LetsEncryptProductionCA,
		Email: cfg.TLS.Email, Agreed: true,
		ListenHost: host, AltHTTPPort: port, AltTLSALPNPort: cfg.TLS.Port,
		DisableHTTPChallenge:    cfg.TLS.Type == "tls-alpn-01" || cfg.TLS.DNS != nil,
		DisableTLSALPNChallenge: cfg.TLS.Type == "http-01" || cfg.TLS.DNS != nil,
	}
	if cfg.TLS.DNS != nil {
		issuer.DNS01Solver = &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{DNSProvider: cfg.TLS.DNS}}
	}
	a.issuer = certmagic.NewACMEIssuer(a.config, issuer)
	a.config.Issuers = []certmagic.Issuer{a.issuer}
	return a, nil
}

func (a *automaticTLS) start() error {
	return a.config.ManageAsync(a.ctx, a.domains)
}

func (a *automaticTLS) close() {
	a.closeOnce.Do(func() { a.cancel(); a.cache.Stop() })
}

func (a *automaticTLS) allowsHost(host string) bool {
	host = strings.ToLower(host)
	for _, domain := range a.domains {
		domain = strings.ToLower(domain)
		if host == domain {
			return true
		}
		if strings.HasPrefix(domain, "*.") {
			dot := strings.IndexByte(host, '.')
			if dot > 0 && host[dot:] == domain[1:] {
				return true
			}
		}
	}
	return false
}

func (a *automaticTLS) tlsConfig() *tls.Config {
	transport := a.config.TLSConfig()
	transport.MinVersion = tls.VersionTLS12
	transport.NextProtos = append([]string{"h2", "http/1.1"}, transport.NextProtos...)
	getCertificate := transport.GetCertificate
	transport.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if !a.allowsHost(hello.ServerName) {
			return nil, fmt.Errorf("TLS server name is not configured")
		}
		return getCertificate(hello)
	}
	return transport
}

// Challenge handling precedes redirects and the application pipeline.
func (a *automaticTLS) httpHandler(next http.Handler) http.Handler {
	return a.issuer.HTTPChallengeHandler(next)
}
