package serverruntime

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
	"github.com/quic-go/quic-go/http3"
)

func TestProtocolsShareSessionBodyAndCompression(t *testing.T) {
	root, cfg, pool := manualFixture(t)
	payload := strings.Repeat("GOX request body ñ ", 150)
	handler := func(ctx *gox.Context) {
		body, err := ctx.Body.ToText()
		if err != nil {
			panic(err)
		}
		ctx.Response.Headers.Set("Content-Type", "text/plain; charset=utf-8")
		ctx.Response.Headers.Set("X-Session", ctx.Session.ID())
		ctx.Writer.WriteRawString(body)
	}
	runtime := newHotBenchmarkRuntime(handler)
	defer runtime.Close()
	runtime.state().Methods = NewMethodPolicy([]string{"GET", "POST"})
	runtime.goxRoutes["/index"].HTTPHandlers["POST"] = handler
	runtime.state().Config.HTTP.Compression = gzipCompressionConfig()
	if err := prepareDynamicCompression(gzipCompressionConfig()); err != nil {
		t.Fatal(err)
	}
	policy, err := NewSessionPolicy("protocols", server.SessionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runtime.state().Session = policy
	runtime.sessionStorage, _, err = OpenSessionRuntime(nil, policy, "")
	if err != nil {
		t.Fatal(err)
	}
	group, err := openRuntimeListeners(cfg, root, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	group.Serve()
	jar, _ := cookiejar.New(nil)
	var session string
	for _, major := range []int{1, 2, 3, 1} {
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, ForceAttemptHTTP2: major == 2}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second}
		if major == 3 {
			h3 := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
			defer h3.Close()
			client.Transport = h3
		}
		req, _ := http.NewRequest("POST", "https://"+group.listeners[1].Addr().String()+"/", strings.NewReader(payload))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Accept-Encoding", "gzip")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.ProtoMajor != major || res.StatusCode != 200 {
			t.Fatalf("protocol=%s status=%d", res.Proto, res.StatusCode)
		}
		if res.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("missing gzip: %v", res.Header)
		}
		reader, err := gzip.NewReader(res.Body)
		if err != nil {
			res.Body.Close()
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		reader.Close()
		res.Body.Close()
		if err != nil || string(body) != payload {
			t.Fatalf("HTTP/%d body length=%d err=%v", major, len(body), err)
		}
		got := res.Header.Get("X-Session")
		if got == "" {
			t.Fatal("session missing")
		}
		if session == "" {
			session = got
			if len(res.Cookies()) < 2 {
				t.Fatal("session cookies missing")
			}
			for _, cookie := range res.Cookies() {
				if !cookie.Secure {
					t.Fatal("insecure HTTPS session cookie")
				}
			}
		} else if got != session {
			t.Fatalf("session changed between protocols: %q != %q", got, session)
		}
	}
}

func TestProtocolRequestCancellation(t *testing.T) {
	for _, major := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(major), func(t *testing.T) {
			root, cfg, pool := manualFixture(t)
			entered, canceled := make(chan struct{}), make(chan struct{})
			group, err := openRuntimeListeners(cfg, root, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
				close(canceled)
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer group.Close()
			group.Serve()
			tcp := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, ForceAttemptHTTP2: major == 2}
			defer tcp.CloseIdleConnections()
			client := &http.Client{Transport: tcp, Timeout: 5 * time.Second}
			if major == 3 {
				transport := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
				defer transport.Close()
				client.Transport = transport
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+group.listeners[1].Addr().String(), nil)
			result := make(chan error, 1)
			go func() {
				res, err := client.Do(req)
				if res != nil {
					res.Body.Close()
				}
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("handler not entered")
			}
			cancel()
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				t.Fatal("request cancellation not propagated")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("client stuck")
			}
		})
	}
}
