package serverruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/gox-lang/gox/server"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type runtimeListeners struct {
	quicServer   *http3.Server
	quicListener *quic.EarlyListener
	packet       net.PacketConn
	servers      []*http.Server
	listeners    []net.Listener
	schemes      []string
	automatic    *automaticTLS
	manual       *manualTLS
	config       server.HTTPConfig
}

func openRuntimeListeners(cfg server.HTTPConfig, root string, handler http.Handler) (*runtimeListeners, error) {
	return openRuntimeListenersWithTLS(cfg, root, handler, newAutomaticTLS)
}

// The factory permits local ACME authorities in integration tests without
// exposing test CA settings or modifying CertMagic process-wide defaults.
func openRuntimeListenersWithTLS(cfg server.HTTPConfig, root string, handler http.Handler, automaticFactory func(server.HTTPConfig) (*automaticTLS, error)) (*runtimeListeners, error) {
	tlsCfg, err := normalizeTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	cfg.TLS = tlsCfg
	var automatic *automaticTLS
	var manual *manualTLS
	httpHandler := handler
	var transport *tls.Config
	if tlsCfg.Enabled {
		if tlsCfg.Type != "manual" {
			automatic, err = automaticFactory(cfg)
			if err != nil {
				return nil, err
			}
			transport = automatic.tlsConfig()
			if tlsCfg.RedirectHTTP {
				policy, err := NewIPResolverPolicy(cfg.ResolverIPForwarded)
				if err != nil {
					automatic.close()
					return nil, err
				}
				httpHandler = httpsRedirectAllowedHandler(handler, automatic.allowsHost, policy)
			}
			httpHandler = automatic.httpHandler(httpHandler)
		} else {
			manual, err = newManualTLS(root, tlsCfg)
			if err != nil {
				return nil, err
			}
			if tlsCfg.RedirectHTTP {
				policy, err := NewIPResolverPolicy(cfg.ResolverIPForwarded)
				if err != nil {
					return nil, err
				}
				httpHandler = httpsRedirectAllowedHandler(handler, manual.allowsHost, policy)
			}
			transport = manual.tlsConfig()
		}
	}
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 3000
	}
	group := &runtimeListeners{automatic: automatic, manual: manual, config: cfg}
	add := func(port int, scheme string, config *tls.Config) error {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("listen %s on %s: %w", scheme, addr, err)
		}
		group.listeners = append(group.listeners, listener)
		serveHandler := handler
		if scheme == "http" {
			serveHandler = httpHandler
		}
		group.servers = append(group.servers, &http.Server{Addr: addr, Handler: serveHandler, TLSConfig: config})
		group.schemes = append(group.schemes, scheme)
		return nil
	}
	if err := add(port, "http", nil); err != nil {
		_ = group.Close()
		return nil, err
	}
	if transport != nil {
		if err := add(tlsCfg.Port, "https", transport); err != nil {
			_ = group.Close()
			return nil, err
		}
		// Bind UDP to the actual HTTPS address, including its selected IP family.
		addr := group.listeners[len(group.listeners)-1].Addr().String()
		packet, err := net.ListenPacket("udp", addr)
		if err != nil {
			_ = group.Close()
			return nil, fmt.Errorf("listen HTTP/3 on %s: %w", addr, err)
		}
		group.packet = packet
		// Do not accept replayable early application requests (0-RTT).
		group.quicListener, err = quic.ListenEarly(packet, http3.ConfigureTLSConfig(transport), &quic.Config{})
		if err != nil {
			_ = group.Close()
			return nil, fmt.Errorf("initialize HTTP/3: %w", err)
		}
		group.quicServer = &http3.Server{Handler: handler}
		secureServer := group.servers[len(group.servers)-1]
		secureServer.Handler = advertiseHTTP3(handler)
	}
	return group, nil
}
func (g *runtimeListeners) Serve() <-chan error {
	results := make(chan error, len(g.servers)+2)
	if g.manual != nil {
		if err := g.manual.start(); err != nil {
			results <- err
			return results
		}
	}
	for i, srv := range g.servers {
		ln := g.listeners[i]
		secure := g.schemes[i] == "https"
		go func() {
			var err error
			if secure {
				err = srv.ServeTLS(ln, "", "")
			} else {
				err = srv.Serve(ln)
			}
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			results <- err
		}()
	}
	if g.quicServer != nil {
		go func() {
			err := g.quicServer.ServeListener(g.quicListener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			results <- err
		}()
	}
	if g.automatic != nil {
		if err := g.automatic.start(); err != nil {
			results <- fmt.Errorf("start automatic TLS: %w", err)
		}
	}
	return results
}
func (g *runtimeListeners) Close() error {
	if g.manual != nil {
		g.manual.close()
	}
	if g.automatic != nil {
		g.automatic.close()
	}
	var errs []error
	if g.quicServer != nil {
		errs = append(errs, g.quicServer.Close())
	}
	if g.quicListener != nil {
		errs = append(errs, g.quicListener.Close())
	}
	if g.packet != nil {
		if err := g.packet.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	for _, s := range g.servers {
		errs = append(errs, s.Close())
	}
	// Also release listeners opened before Serve registered them.
	for _, ln := range g.listeners {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (g *runtimeListeners) Shutdown(ctx context.Context) error {
	errs := make(chan error, len(g.servers)+1)
	var wg sync.WaitGroup
	for _, s := range g.servers {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Shutdown(ctx) }()
	}
	if g.quicServer != nil {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- g.quicServer.Shutdown(ctx) }()
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	all = append(all, g.Close())
	return errors.Join(all...)
}

// advertiseHTTP3 is installed only after QUIC has successfully bound.
// Public HTTPS uses 443 even when the internal port is forwarded.
func advertiseHTTP3(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", `h3=":443"; ma=86400`)
		next.ServeHTTP(w, r)
	})
}
