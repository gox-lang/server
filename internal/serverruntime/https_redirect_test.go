package serverruntime

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func TestHTTPSRedirectHostAndPath(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{"example.com", "*.example.org"}, IPAddresses: []net.IP{net.ParseIP("::1")}}
	var calls int
	handler := httpsRedirectHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }), cert, nil)
	for _, tc := range []struct{ host, location string }{
		{"example.com:8080", "https://example.com/a%2Fb?next=%2F&x=1"},
		{"EXAMPLE.COM", "https://example.com/a%2Fb?next=%2F&x=1"},
		{"one.example.org", "https://one.example.org/a%2Fb?next=%2F&x=1"},
		{"[::1]:8080", "https://[::1]/a%2Fb?next=%2F&x=1"},
		{"evil.test", ""}, {"a.b.example.org", ""}, {"example.com:bad", ""},
		{"example.com:0", ""}, {"example.com:65536", ""}, {"example.com:", ""},
		{"example.com@evil.test", ""}, {"example.com/path", ""}, {"::1", ""},
		{"[::1]evil", ""}, {"", ""},
	} {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://example.com/a%2Fb?next=%2F&x=1", strings.NewReader("body"))
			req.Host = tc.host
			req.Header.Set("X-Forwarded-Host", "evil.test")
			req.Header.Set("X-Forwarded-Proto", "https")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			want := http.StatusPermanentRedirect
			if tc.location == "" {
				want = http.StatusBadRequest
			}
			if rec.Code != want || rec.Header().Get("Location") != tc.location {
				t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("redirect created cookies")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("application invoked %d times", calls)
	}
}

func TestRequestProtocolTrust(t *testing.T) {
	policy, err := NewIPResolverPolicy(server.ResolverIPForwardedConfig{TrustedProxies: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, peer, header, target, want string }{
		{"spoof", "192.0.2.1:1000", "https", "http://example.com/", "http"},
		{"trusted", "127.0.0.1:1000", "https", "http://example.com/", "https"},
		{"list", "127.0.0.1:1000", "https, http", "http://example.com/", "http"},
		{"unknown", "127.0.0.1:1000", "garbage", "http://example.com/", "http"},
		{"TLS cannot downgrade", "127.0.0.1:1000", "http", "https://example.com/", "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.target, nil)
			req.RemoteAddr = tc.peer
			req.Header.Set("X-Forwarded-Proto", tc.header)
			// An absolute-form request must not override the actual transport.
			req.URL.Scheme = "https"
			if tc.want == "https" {
				req.URL.Scheme = "http"
			}
			got := buildRequestWithBody(req, gox.NewBody(nil, ""), policy)
			if got.IsSecure() != (tc.want == "https") || got.URL.Scheme() != tc.want {
				t.Fatalf("inconsistent secure request: %+v", got)
			}
		})
	}
}

func TestManualHTTPSRedirectAndTrustedProxy(t *testing.T) {
	root, cfg, pool := manualFixture(t)
	cfg.TLS.RedirectHTTP = true
	cfg.ResolverIPForwarded.TrustedProxies = []string{"127.0.0.1"}
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) })
	group, err := openRuntimeListeners(cfg, root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	group.Serve()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second * 5, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, tc := range []struct {
		scheme, proto string
		index, status int
	}{
		{"http", "", 0, 308}, {"http", "https", 0, 204}, {"https", "http", 1, 204},
	} {
		req, _ := http.NewRequest("POST", tc.scheme+"://"+group.listeners[tc.index].Addr().String()+"/a%2Fb?q=1", nil)
		req.Header.Set("X-Forwarded-Proto", tc.proto)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Fatalf("status=%d", res.StatusCode)
		}
		if tc.status == 308 && res.Header.Get("Location") != "https://127.0.0.1/a%2Fb?q=1" {
			t.Fatal(res.Header)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("application calls=%d", calls.Load())
	}
}

func TestManualTLSWebSocket(t *testing.T) {
	root, cfg, pool := manualFixture(t)
	cfg.TLS.RedirectHTTP = true
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.Close()
	connected := make(chan bool, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath, State: GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("connect", func(bool) { connected <- true })
		},
	}
	group, err := openRuntimeListeners(cfg, root, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	group.Serve()
	authority := group.listeners[1].Addr().String()
	// An HTTP/2 connection and Gorilla's HTTP/1.1 upgrade must coexist.
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	page, err := client.Get("https://" + authority + "/")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.ProtoMajor != 2 {
		t.Fatalf("expected HTTP/2 before WSS, got %s", page.Proto)
	}
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: pool}, HandshakeTimeout: 5 * time.Second}
	conn, res, err := dialer.Dial("wss://"+authority+"/_gox/websocket/handshake", http.Header{"Origin": []string{"https://" + authority}})
	if err != nil {
		t.Fatalf("WSS failed: %v (%v)", err, res)
	}
	defer conn.Close()
	if res.ProtoMajor != 1 || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("WebSocket upgrade: %s status=%d", res.Proto, res.StatusCode)
	}
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("no connect event")
	}
	if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCookiesFollowTransportSecurity(t *testing.T) {
	for _, secure := range []bool{false, true} {
		response := gox.NewResponse()
		writeCookie(response, "SessionID", "id", secure)
		writeCookie(response, "SessionToken", "rotated", secure)
		deleteCookie(response, "SessionID", secure)
		values, _ := response.Headers.GetAll("Set-Cookie")
		cookies := (&http.Response{Header: http.Header{"Set-Cookie": values}}).Cookies()
		if len(cookies) != 3 {
			t.Fatal(values)
		}
		for _, cookie := range cookies {
			if cookie.Secure != secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("unexpected cookie: %+v", cookie)
			}
		}
	}
}

func TestSecureWebSocketOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "https://example.com/_gox/websocket/handshake", nil)
	for _, origin := range []string{"http://example.com", "https://evil.test", "https://user@example.com", "https://example.com/path", "null"} {
		req.Header.Set("Origin", origin)
		if sameWebSocketOrigin(req) {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	req.Header.Set("Origin", "https://example.com")
	if !sameWebSocketOrigin(req) {
		t.Fatal("rejected HTTPS same origin")
	}
	req.TLS = nil
	if !sameWebSocketOrigin(req, "https") {
		t.Fatal("rejected trusted proxy HTTPS")
	}
}
