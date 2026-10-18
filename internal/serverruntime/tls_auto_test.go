package serverruntime

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/gox-lang/gox/server"
)

func TestAutomaticTLSModesAndStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOX_HOME", home)
	for _, mode := range []string{"auto", "http-01", "tls-alpn-01", "dns-01", "auto-dns"} {
		t.Run(mode, func(t *testing.T) {
			cfg := server.HTTPConfig{Host: "127.0.0.1", Port: 8080, TLS: server.TLSConfig{Enabled: true, Type: mode, Port: 8443, Domains: []string{"EXAMPLE.COM"}}}
			if mode == "dns-01" || mode == "auto-dns" {
				cfg.TLS.DNS = &configDNSProvider{}
			}
			if mode == "auto-dns" {
				cfg.TLS.Type = "auto"
				cfg.TLS.Domains = append(cfg.TLS.Domains, "*.example.org")
			}
			a, err := newAutomaticTLS(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer a.close()
			if a.issuer.DisableHTTPChallenge != (mode == "tls-alpn-01" || cfg.TLS.DNS != nil) {
				t.Fatal("HTTP challenge mismatch")
			}
			if a.issuer.DisableTLSALPNChallenge != (mode == "http-01" || cfg.TLS.DNS != nil) {
				t.Fatal("ALPN challenge mismatch")
			}
			if (a.issuer.DNS01Solver != nil) != (cfg.TLS.DNS != nil) {
				t.Fatal("DNS solver mismatch")
			}
			if a.config.OnDemand != nil {
				t.Fatal("unrestricted on-demand issuance")
			}
			if a.issuer.AltHTTPPort != 8080 || a.issuer.AltTLSALPNPort != 8443 {
				t.Fatal("incorrect internal challenge ports")
			}
			if a.config.Storage.(*certmagic.FileStorage).Path != filepath.Join(home, ".tls") {
				t.Fatal("incorrect storage")
			}
			if !a.allowsHost("example.com") || a.allowsHost("evil.example.com") {
				t.Fatal("host allowlist")
			}
			if mode == "auto-dns" && (!a.allowsHost("one.example.org") || a.allowsHost("a.b.example.org") || a.allowsHost("example.org")) {
				t.Fatal("wildcard matching")
			}
			if _, err := a.tlsConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: "attacker.test"}); err == nil {
				t.Fatal("unconfigured SNI accepted")
			}
		})
	}
	info, err := os.Stat(filepath.Join(home, ".tls"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("storage mode: %v", info.Mode())
	}
}

func TestAutomaticTLSFailedListenerCleansUp(t *testing.T) {
	t.Setenv("GOX_HOME", t.TempDir())
	// Invalid bind address fails before any ACME operation.
	cfg := server.HTTPConfig{Host: "invalid host", Port: 8080, TLS: server.TLSConfig{Enabled: true, Type: "auto", Port: 8443, Domains: []string{"example.com"}}}
	var automatic *automaticTLS
	_, err := openRuntimeListenersWithTLS(cfg, t.TempDir(), http.NotFoundHandler(), func(cfg server.HTTPConfig) (*automaticTLS, error) {
		var err error
		automatic, err = newAutomaticTLS(cfg)
		return automatic, err
	})
	if err == nil {
		t.Fatal("expected bind failure")
	}
	if automatic == nil || automatic.ctx.Err() == nil {
		t.Fatal("ACME context was not cancelled")
	}
	automatic.close() // Idempotent cleanup.
}

func TestAutomaticTLSStorageFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".tls"), []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := newAutomaticTLS(server.HTTPConfig{})
	if err == nil {
		t.Fatal("invalid storage accepted")
	}
}

func TestAutomaticTLSCloseCancelsIssuance(t *testing.T) {
	t.Setenv("GOX_HOME", t.TempDir())
	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	ca := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
		select {
		case cancelled <- struct{}{}:
		default:
		}
	}))
	defer ca.Close()
	root, cfg, _ := manualFixture(t)
	cfg.TLS.Type = "auto"
	cfg.TLS.CertFile = ""
	cfg.TLS.KeyFile = ""
	cfg.TLS.Domains = []string{"cancel.gox.test"}
	group, err := openRuntimeListenersWithTLS(cfg, root, http.NotFoundHandler(), func(cfg server.HTTPConfig) (*automaticTLS, error) {
		a, err := newAutomaticTLS(cfg)
		if err != nil {
			return nil, err
		}
		a.issuer = certmagic.NewACMEIssuer(a.config, certmagic.ACMEIssuer{CA: ca.URL + "/dir", Agreed: true})
		a.config.Issuers = []certmagic.Issuer{a.issuer}
		return a, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	group.Serve()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("ACME request did not start")
	}
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("ACME request not cancelled")
	}
	assertPortsReleased(t, group)
}
