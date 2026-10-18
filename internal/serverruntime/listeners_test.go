package serverruntime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func manualFixture(t *testing.T) (string, server.HTTPConfig, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(root, "cert.pem"), cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}), 0600); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(cert)
	cfg := server.HTTPConfig{Host: "127.0.0.1", Port: freePort(t), TLS: server.TLSConfig{Enabled: true, Type: "manual", Port: freePort(t), CertFile: "cert.pem", KeyFile: "key.pem"}}
	for cfg.Port == cfg.TLS.Port {
		cfg.TLS.Port = freePort(t)
	}
	return root, cfg, pool
}
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}
func assertPortsReleased(t *testing.T, g *runtimeListeners) {
	t.Helper()
	if g.packet != nil {
		packet, err := net.ListenPacket("udp", g.packet.LocalAddr().String())
		if err != nil {
			t.Fatal("UDP port not released:", err)
		}
		packet.Close()
	}
	for _, old := range g.listeners {
		ln, err := net.Listen("tcp", old.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		ln.Close()
	}
}
func TestManualHTTPSUsesRuntimePipelineAndShutsDown(t *testing.T) {
	root, cfg, pool := manualFixture(t)
	runtime := newHotBenchmarkRuntime(func(ctx *gox.Context) { ctx.Writer.WriteRawString("manual TLS works") })
	t.Cleanup(func() { runtime.Close() })
	g, err := openRuntimeListeners(cfg, root, runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	results := g.Serve()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for i, ln := range g.listeners {
		res, err := client.Get(g.schemes[i] + "://" + ln.Addr().String() + "/")
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || string(data) != "manual TLS works" {
			t.Fatalf("status=%d body=%q err=%v", res.StatusCode, data, err)
		}
		if i == 1 && res.TLS == nil {
			t.Fatal("missing TLS handshake")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for range g.servers {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("serve did not stop")
		}
	}
	assertPortsReleased(t, g)
}
func TestManualHTTPSFailureReleasesHTTPPort(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	occupied, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.TLS.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, err := openRuntimeListeners(cfg, root, http.NotFoundHandler()); err == nil {
		t.Fatal("expected port conflict")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		t.Fatal("HTTP port leaked:", err)
	}
	ln.Close()
}
func TestManualHTTPSRejectsBadKeyBeforeOpeningPorts(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	otherRoot, _, _ := manualFixture(t)
	cfg.TLS.KeyFile = filepath.Join(otherRoot, "key.pem")
	if _, err := openRuntimeListeners(cfg, root, http.NotFoundHandler()); err == nil {
		t.Fatal("mismatched key accepted")
	}
	cfg.TLS.KeyFile = "missing.pem"
	if _, err := openRuntimeListeners(cfg, root, http.NotFoundHandler()); err == nil {
		t.Fatal("missing key accepted")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}
func TestManualHTTPSRestartReleasesBothPorts(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	g, err := openRuntimeListeners(cfg, root, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.Serve()
	r := &Runtime{}
	if err := r.prepareRuntimeRestart(g, RestartRequest{}); err != nil {
		t.Fatal(err)
	}
	assertPortsReleased(t, g)
}

func TestHTTPOnlyListenerPreserved(t *testing.T) {
	cfg := server.HTTPConfig{Host: "127.0.0.1", Port: freePort(t)}
	g, err := openRuntimeListeners(cfg, t.TempDir(), http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if len(g.servers) != 1 || g.schemes[0] != "http" {
		t.Fatal("unexpected HTTPS listener")
	}
}

func TestManualHTTPSShutdownDeadlineClosesConnections(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	g, err := openRuntimeListeners(cfg, root, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	defer close(release)
	g.Serve()
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := http.Get("http://" + g.listeners[0].Addr().String())
		if err == nil {
			res.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown error=%v", err)
	}
	assertPortsReleased(t, g)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connection not closed")
	}
}
