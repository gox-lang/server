//go:build acmeintegration

package serverruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/gox-lang/gox/server"
	"github.com/libdns/libdns"
	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Run with GOX_TEST_PEBBLE pointing to the official Pebble executable.
// All validation is real, but the CA, DNS, listeners and storage are local.
func TestACMELocalAuthority(t *testing.T) {
	binary := os.Getenv("GOX_TEST_PEBBLE")
	if binary == "" {
		t.Fatal("set GOX_TEST_PEBBLE to the Pebble executable")
	}
	for _, mode := range []string{"http-01", "tls-alpn-01", "auto", "dns-01", "auto-dns"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("GOX_HOME", home)
			root, cfg, authorityRoots := manualFixture(t)
			provider := &localDNSProvider{records: make(map[string][]string)}
			resolver := startACMEDNS(t, provider)
			usedPorts := map[int]bool{cfg.Port: true, cfg.TLS.Port: true}
			nextAddress := func() string {
				for {
					port := freePort(t)
					if !usedPorts[port] {
						usedPorts[port] = true
						return fmt.Sprintf("127.0.0.1:%d", port)
					}
				}
			}
			authorityAddr := nextAddress()
			managementAddr := nextAddress()
			pebbleConfig := map[string]any{"pebble": map[string]any{
				"listenAddress": authorityAddr, "managementListenAddress": managementAddr,
				"httpPort": cfg.Port, "tlsPort": cfg.TLS.Port,
				"certificate": filepath.Join(root, "cert.pem"), "privateKey": filepath.Join(root, "key.pem"),
				"keyAlgorithm": "ecdsa",
			}}
			data, _ := json.Marshal(pebbleConfig)
			configPath := filepath.Join(root, "pebble.json")
			if err := os.WriteFile(configPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			logFile, err := os.Create(filepath.Join(root, "pebble.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer logFile.Close()
			cmd := exec.Command(binary, "-config", configPath, "-dnsserver", resolver)
			cmd.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_VA_ALWAYS_VALID=0", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_AUTHZREUSE=0")
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			var stopOnce sync.Once
			stopCA := func() { stopOnce.Do(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) }
			defer stopCA()
			t.Cleanup(func() {
				if t.Failed() {
					data, _ := os.ReadFile(logFile.Name())
					t.Log(string(data))
				}
			})
			caTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: authorityRoots}}
			defer caTransport.CloseIdleConnections()
			caClient := &http.Client{Transport: caTransport, Timeout: time.Second}
			var rootPEM []byte
			waitACME(t, 10*time.Second, func() bool {
				res, err := caClient.Get("https://" + managementAddr + "/roots/0")
				if err != nil {
					return false
				}
				defer res.Body.Close()
				rootPEM, _ = io.ReadAll(res.Body)
				return res.StatusCode == 200
			})
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(rootPEM) {
				t.Fatal("missing Pebble root")
			}
			domain := strings.ReplaceAll(mode, "-", "") + ".gox.test"
			cfg.TLS = server.TLSConfig{Enabled: true, Type: mode, Port: cfg.TLS.Port, Domains: []string{domain}, RedirectHTTP: true}
			if mode == "dns-01" || mode == "auto-dns" {
				cfg.TLS.DNS = provider
				cfg.TLS.Domains = []string{"*.gox.test"}
			}
			if mode == "auto-dns" {
				cfg.TLS.Type = "auto"
			}
			testRenewal := true
			factory := func(cfg server.HTTPConfig) (*automaticTLS, error) {
				a, err := newAutomaticTLS(cfg)
				if err != nil {
					return nil, err
				}
				// No process-wide defaults or production configuration are changed.
				template := *a.issuer
				template.CA = "https://" + authorityAddr + "/dir"
				template.TestCA = ""
				template.TrustedRoots = authorityRoots
				template.CertObtainTimeout = 20 * time.Second
				a.issuer = certmagic.NewACMEIssuer(a.config, template)
				a.config.Issuers = []certmagic.Issuer{a.issuer}
				if testRenewal {
					a.config.DisableARI = true
					a.config.RenewalWindowRatio = 1
				}
				if solver, ok := a.issuer.DNS01Solver.(*certmagic.DNS01Solver); ok {
					solver.Resolvers = []string{resolver}
					// Pebble still validates actual TXT records. Authoritative DNS checks
					// in CertMagic require port 53; this isolated test uses a random port.
					solver.PropagationTimeout = -1
				}
				return a, nil
			}
			var applicationCalls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { applicationCalls.Add(1); fmt.Fprint(w, "ACME works") })
			group, err := openRuntimeListenersWithTLS(cfg, root, handler, factory)
			if err != nil {
				t.Fatal(err)
			}
			defer group.Close()
			group.Serve()
			addr := group.listeners[1].Addr().String()
			var serial string
			waitACME(t, 35*time.Second, func() bool {
				conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{RootCAs: roots, ServerName: domain})
				if err != nil {
					return false
				}
				serial = conn.ConnectionState().PeerCertificates[0].SerialNumber.String()
				conn.Close()
				return true
			})
			if applicationCalls.Load() != 0 {
				t.Fatalf("ACME validation entered application: %d", applicationCalls.Load())
			}
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			}}
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			res, err := client.Get("https://" + domain + "/")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			transport.CloseIdleConnections()
			if string(body) != "ACME works" {
				t.Fatal(string(body))
			}
			checkHTTP3 := func(expectedSerial string) {
				t.Helper()
				transport := &http3.Transport{
					TLSClientConfig: &tls.Config{RootCAs: roots},
					Dial: func(ctx context.Context, _ string, tlsCfg *tls.Config, quicCfg *quic.Config) (*quic.Conn, error) {
						return quic.DialAddrEarly(ctx, addr, tlsCfg, quicCfg)
					},
				}
				defer transport.Close()
				client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
				res, err := client.Get("https://" + domain + "/")
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				if err != nil || res.ProtoMajor != 3 || string(body) != "ACME works" {
					t.Fatalf("HTTP/3: protocol=%s body=%q err=%v", res.Proto, body, err)
				}
				if res.TLS.PeerCertificates[0].SerialNumber.String() != expectedSerial {
					t.Fatal("HTTP/3 certificate differs from TCP")
				}
			}
			checkHTTP3(serial)
			// A renewal obtains and caches a replacement while listeners stay open.
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if err := group.automatic.cache.RenewManagedCertificates(ctx); err != nil {
				t.Fatal(err)
			}
			var renewed string
			waitACME(t, 25*time.Second, func() bool {
				conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{RootCAs: roots, ServerName: domain})
				if err != nil {
					return false
				}
				defer conn.Close()
				renewed = conn.ConnectionState().PeerCertificates[0].SerialNumber.String()
				return renewed != serial
			})

			checkHTTP3(renewed)
			if cfg.TLS.DNS != nil {
				provider.mu.Lock()
				remaining := len(provider.records)
				appended, deleted := provider.appended, provider.deleted
				provider.mu.Unlock()
				if appended == 0 || appended != deleted || remaining != 0 {
					t.Fatalf("DNS lifecycle: append=%d delete=%d remaining=%d", appended, deleted, remaining)
				}
			}
			// Restart with the CA offline: persisted certificates must suffice.
			if err := group.Close(); err != nil {
				t.Fatal(err)
			}
			stopCA()
			testRenewal = false
			group2, err := openRuntimeListenersWithTLS(cfg, root, handler, factory)
			if err != nil {
				t.Fatal(err)
			}
			defer group2.Close()
			group2.Serve()
			waitACME(t, 5*time.Second, func() bool {
				conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", group2.listeners[1].Addr().String(), &tls.Config{RootCAs: roots, ServerName: domain})
				if err != nil {
					return false
				}
				defer conn.Close()
				return conn.ConnectionState().PeerCertificates[0].SerialNumber.String() == renewed
			})
			checkHTTP3(renewed)
		})
	}
}

func waitACME(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for local ACME")
}

type localDNSProvider struct {
	mu                sync.Mutex
	records           map[string][]string
	appended, deleted int
}

func (p *localDNSProvider) AppendRecords(_ context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, record := range records {
		rr := record.RR()
		name := dns.Fqdn(libdns.AbsoluteName(rr.Name, zone))
		p.records[name] = append(p.records[name], rr.Data)
		p.appended++
	}
	return records, nil
}
func (p *localDNSProvider) DeleteRecords(_ context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, record := range records {
		rr := record.RR()
		name := dns.Fqdn(libdns.AbsoluteName(rr.Name, zone))
		values := p.records[name]
		for i, value := range values {
			if value == rr.Data {
				values = append(values[:i], values[i+1:]...)
				break
			}
		}
		if len(values) == 0 {
			delete(p.records, name)
		} else {
			p.records[name] = values
		}
		p.deleted++
	}
	return records, nil
}
func startACMEDNS(t *testing.T, p *localDNSProvider) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp", listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		msg := new(dns.Msg)
		msg.SetReply(req)
		msg.Authoritative = true
		for _, q := range req.Question {
			h := dns.RR_Header{Name: q.Name, Rrtype: q.Qtype, Class: dns.ClassINET, Ttl: 1}
			switch q.Qtype {
			case dns.TypeA:
				msg.Answer = append(msg.Answer, &dns.A{Hdr: h, A: net.ParseIP("127.0.0.1")})
			case dns.TypeSOA:
				if q.Name == "gox.test." {
					msg.Answer = append(msg.Answer, &dns.SOA{Hdr: h, Ns: "ns.gox.test.", Mbox: "admin.gox.test.", Serial: 1, Refresh: 60, Retry: 60, Expire: 60, Minttl: 1})
				}
			case dns.TypeTXT:
				p.mu.Lock()
				for _, value := range p.records[q.Name] {
					msg.Answer = append(msg.Answer, &dns.TXT{Hdr: h, Txt: []string{value}})
				}
				p.mu.Unlock()
			}
		}
		_ = w.WriteMsg(msg)
	})
	tcp := &dns.Server{Listener: listener, Handler: handler}
	udp := &dns.Server{PacketConn: packet, Handler: handler}
	go tcp.ActivateAndServe()
	go udp.ActivateAndServe()
	t.Cleanup(func() { tcp.Shutdown(); udp.Shutdown() })
	return listener.Addr().String()
}
