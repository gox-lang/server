package serverruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/quic-go/quic-go/http3"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestHTTP3UDPConflictReleasesTCP(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.TLS.Port))
	occupied, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if group, err := openRuntimeListeners(cfg, root, http.NotFoundHandler()); err == nil {
		group.Close()
		t.Fatal("occupied UDP port accepted")
	}
	for _, port := range []int{cfg.Port, cfg.TLS.Port} {
		ln, err := net.Listen("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(port)))
		if err != nil {
			t.Fatal("TCP leaked:", err)
		}
		ln.Close()
	}
}

func TestHTTP3CloseBeforeServeReleasesUDP(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	group, err := openRuntimeListeners(cfg, root, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	addr := group.packet.LocalAddr().String()
	if err := group.Close(); err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal("UDP leaked:", err)
	}
	packet.Close()
	assertPortsReleased(t, group)
}

func TestHTTP3ReloadRejectsOccupiedUDP(t *testing.T) {
	root, cfg, pool := manualFixture(t)
	group, err := openRuntimeListeners(cfg, root, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("still serving")) }))
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	group.Serve()
	if err := group.validateReplacement(cfg, root); err != nil {
		t.Fatal("own ports rejected:", err)
	}
	occupied, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	candidate := cfg
	candidate.TLS.Port = occupied.LocalAddr().(*net.UDPAddr).Port
	runtime := &Runtime{ProjectRoot: root, activeListeners: group}
	if restart, err := runtime.prepareTransportReload(candidate); err == nil || restart {
		t.Fatalf("restart=%v error=%v", restart, err)
	}
	transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	defer transport.Close()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	res, err := client.Get("https://" + group.listeners[1].Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || string(body) != "still serving" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestHTTP3ShutdownInFlight(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(fmt.Sprint("forced=", forced), func(t *testing.T) {
			root, cfg, pool := manualFixture(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			exited := make(chan struct{})
			group, err := openRuntimeListeners(cfg, root, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(exited)
				close(entered)
				select {
				case <-release:
					w.Write([]byte("finished"))
				case <-r.Context().Done():
				}
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer group.Close()
			defer close(release)
			results := group.Serve()
			transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
			defer transport.Close()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			request := make(chan error, 1)
			go func() {
				res, err := client.Get("https://" + group.listeners[1].Addr().String())
				if err == nil {
					var body []byte
					body, err = io.ReadAll(res.Body)
					res.Body.Close()
					if string(body) != "finished" && err == nil {
						err = fmt.Errorf("body=%q", body)
					}
				}
				request <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if forced {
				cancel()
			}
			shutdown := make(chan error, 1)
			go func() { shutdown <- group.Shutdown(ctx) }()
			if !forced {
				select {
				case err := <-shutdown:
					t.Fatalf("shutdown discarded active request: %v", err)
				case <-time.After(50 * time.Millisecond):
				}
				release <- struct{}{}
			}
			select {
			case err := <-shutdown:
				if forced && !errors.Is(err, context.Canceled) {
					t.Fatalf("shutdown=%v", err)
				}
				if !forced && err != nil {
					t.Fatal(err)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("shutdown stuck")
			}
			select {
			case err := <-request:
				if !forced && err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request stuck")
			}
			select {
			case <-exited:
			case <-time.After(time.Second):
				t.Fatal("handler not canceled")
			}
			for range len(group.servers) + 1 {
				select {
				case err := <-results:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("serve not stopped")
				}
			}
			assertPortsReleased(t, group)
		})
	}
}

func TestHTTP3RestartReopensSamePorts(t *testing.T) {
	root, cfg, pool := manualFixture(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	group, err := openRuntimeListeners(cfg, root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	group.Serve()
	check := func(g *runtimeListeners) {
		t.Helper()
		transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
		defer transport.Close()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		res, err := client.Get("https://" + g.listeners[1].Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil || string(body) != "ok" {
			t.Fatalf("body=%q err=%v", body, err)
		}
	}
	check(group)
	runtime := &Runtime{}
	if err := runtime.prepareRuntimeRestart(group, RestartRequest{}); err != nil {
		t.Fatal(err)
	}
	assertPortsReleased(t, group)
	replacement, err := openRuntimeListeners(cfg, root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	replacement.Serve()
	check(replacement)
}

func TestHTTP3ReloadRejectsOtherAddressSamePort(t *testing.T) {
	root, cfg, _ := manualFixture(t)
	group, err := openRuntimeListeners(cfg, root, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	occupied, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.2", strconv.Itoa(cfg.TLS.Port)))
	if err != nil {
		t.Skipf("second loopback address unavailable: %v", err)
	}
	defer occupied.Close()
	candidate := cfg
	candidate.Host = "127.0.0.2"
	if err := group.validateReplacement(candidate, root); err == nil {
		t.Fatal("another address's UDP bind was mistaken for our own")
	}
}
