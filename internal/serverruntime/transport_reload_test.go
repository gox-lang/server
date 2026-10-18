package serverruntime

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gox-lang/gox/server"
	"github.com/quic-go/quic-go/http3"
)

func TestManualCertificateReloadKeepsListenersAndRejectsInvalidPair(t *testing.T) {
	root, cfg, pool := manualFixture(t)
	nextRoot, _, _ := manualFixture(t)
	nextCert, err := os.ReadFile(filepath.Join(nextRoot, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	nextKey, err := os.ReadFile(filepath.Join(nextRoot, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool.AppendCertsFromPEM(nextCert)
	// Absolute paths exercise certificates outside the project being served.
	cfg.TLS.CertFile = filepath.Join(root, "cert.pem")
	cfg.TLS.KeyFile = filepath.Join(root, "key.pem")
	g, err := openRuntimeListeners(cfg, t.TempDir(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "alive") }))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.Serve()
	address := g.listeners[1].Addr().String()
	readCert := func() []byte {
		t.Helper()
		conn, err := tls.Dial("tcp", address, &tls.Config{RootCAs: pool, ServerName: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		cert := conn.ConnectionState().PeerCertificates[0].Raw
		transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
		defer transport.Close()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		res, err := client.Get("https://" + address)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if _, err := io.ReadAll(res.Body); err != nil {
			t.Fatal(err)
		}
		// The watcher may replace the certificate between the two handshakes.
		current := g.manual.current.Load().leaf.Raw
		if !bytes.Equal(res.TLS.PeerCertificates[0].Raw, cert) && !bytes.Equal(res.TLS.PeerCertificates[0].Raw, current) {
			t.Fatal("HTTP/3 has stale certificate")
		}
		return cert
	}
	original := readCert()
	// Invalid PEM, then a valid certificate with the previous private key.
	atomicPEM(t, cfg.TLS.CertFile, []byte("broken"))
	time.Sleep(250 * time.Millisecond)
	if !bytes.Equal(original, readCert()) {
		t.Fatal("invalid PEM replaced active certificate")
	}
	atomicPEM(t, cfg.TLS.CertFile, nextCert)
	time.Sleep(250 * time.Millisecond)
	if !bytes.Equal(original, readCert()) {
		t.Fatal("mismatched key replaced active certificate")
	}
	atomicPEM(t, cfg.TLS.KeyFile, nextKey)
	eventuallyTLS(t, func() bool { return !bytes.Equal(original, readCert()) })
	if g.listeners[1].Addr().String() != address {
		t.Fatal("listener changed")
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	assertPortsReleased(t, g)
}

func atomicPEM(t *testing.T, path string, data []byte) {
	t.Helper()
	temp := path + ".new"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, path); err != nil {
		t.Fatal(err)
	}
}
func eventuallyTLS(t *testing.T, ready func() bool) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("TLS update did not complete")
}

func TestManualCertificateSymlinkRotation(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	nextRoot, _, _ := manualFixture(t)
	for _, name := range []string{"cert.pem", "key.pem"} {
		if err := os.Symlink(filepath.Join(root, name), filepath.Join(root, "live-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	cfg.TLS.CertFile = "live-cert.pem"
	cfg.TLS.KeyFile = "live-key.pem"
	m, err := newManualTLS(root, cfg.TLS)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if err := m.start(); err != nil {
		t.Fatal(err)
	}
	old := m.current.Load()
	for _, name := range []string{"cert.pem", "key.pem"} {
		temp := filepath.Join(root, "new-"+name)
		if err := os.Symlink(filepath.Join(nextRoot, name), temp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, filepath.Join(root, "live-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	eventuallyTLS(t, func() bool { return !bytes.Equal(old.pair.Certificate[0], m.current.Load().pair.Certificate[0]) })
}

func TestTransportReloadRejectsInvalidBeforeRestart(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	g, err := openRuntimeListeners(cfg, root, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.Serve()
	runtime := &Runtime{ProjectRoot: root, activeListeners: g}
	bad := cfg
	bad.TLS.KeyFile = "missing.pem"
	if restart, err := runtime.prepareTransportReload(bad); err == nil || restart {
		t.Fatalf("accepted invalid key: %v", err)
	}
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	bad = cfg
	bad.TLS.Port = busy.Addr().(*net.TCPAddr).Port
	if restart, err := runtime.prepareTransportReload(bad); err == nil || restart {
		t.Fatalf("accepted occupied port: %v", err)
	}
	bad = cfg
	bad.Host = "192.0.2.1"
	if restart, err := runtime.prepareTransportReload(bad); err == nil || restart {
		t.Fatalf("accepted nonlocal bind: %v", err)
	}
	select {
	case <-runtime.restartRequests():
		t.Fatal("invalid change requested restart")
	default:
	}
	res, err := http.Get("http://" + g.listeners[0].Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	good := cfg
	good.TLS.RedirectHTTP = true
	restart, err := runtime.prepareTransportReload(good)
	if err != nil || !restart {
		t.Fatalf("valid change not scheduled: %v", err)
	}
	select {
	case request := <-runtime.restartRequests():
		if request.Reason != RestartReasonHTTPConfigChanged {
			t.Fatal(request)
		}
	default:
		t.Fatal("missing restart")
	}
	if g.config.TLS.RedirectHTTP {
		t.Fatal("active listener config mutated before restart")
	}
}

func TestTransportComparison(t *testing.T) {
	base := server.HTTPConfig{TLS: server.TLSConfig{Enabled: true, Type: "auto", Port: 443, Domains: []string{"example.com"}}}
	same := base
	same.Host = "localhost"
	same.Port = 3000
	if !sameTransport(base, same) {
		t.Fatal("equivalent default ports changed")
	}
	same.TLS.Enabled = false
	if sameTransport(base, same) {
		t.Fatal("TLS disable ignored")
	}
	a := server.HTTPConfig{}
	b := a
	b.TLS.Domains = []string{"unused.test"}
	if !sameTransport(a, b) {
		t.Fatal("inactive TLS caused restart")
	}
	a = base
	b = base
	a.TLS.DNS = &configDNSProvider{}
	b.TLS.DNS = a.TLS.DNS
	if !sameTransport(a, b) {
		t.Fatal("same DNS identity changed")
	}
	a.TLS.RedirectHTTP = true
	b = a
	b.ResolverIPForwarded.TrustedProxies = []string{"127.0.0.1"}
	if sameTransport(a, b) {
		t.Fatal("redirect proxy policy change ignored")
	}
}

func TestTransportReloadRejectsOtherAddressSamePort(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	g, err := openRuntimeListeners(cfg, root, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	busy, err := net.Listen("tcp", net.JoinHostPort("127.0.0.2", strconv.Itoa(cfg.Port)))
	if err != nil {
		t.Skipf("second loopback address unavailable: %v", err)
	}
	defer busy.Close()
	cfg.Host = "127.0.0.2"
	if err := g.validateReplacement(cfg, root); err == nil {
		t.Fatal("occupied address accepted merely because port number matched")
	}
}

func TestManualCertificateChangedBeforeWatcherStarts(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	next, _, _ := manualFixture(t)
	m, err := newManualTLS(root, cfg.TLS)
	if err != nil {
		t.Fatal(err)
	}
	old := m.current.Load()
	for _, name := range []string{"cert.pem", "key.pem"} {
		data, err := os.ReadFile(filepath.Join(next, name))
		if err != nil {
			t.Fatal(err)
		}
		atomicPEM(t, filepath.Join(root, name), data)
	}
	if err := m.start(); err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if bytes.Equal(old.pair.Certificate[0], m.current.Load().pair.Certificate[0]) {
		t.Fatal("startup served stale certificate")
	}
}
