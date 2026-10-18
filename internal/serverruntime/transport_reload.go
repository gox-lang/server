package serverruntime

import (
	"fmt"
	"net"
	"reflect"
	"strconv"

	"github.com/gox-lang/gox/server"
)

func sameTransport(a, b server.HTTPConfig) bool {
	host := func(s string) string {
		if s == "" {
			return "localhost"
		}
		return s
	}
	port := func(n int) int {
		if n == 0 {
			return 3000
		}
		return n
	}
	if host(a.Host) != host(b.Host) || port(a.Port) != port(b.Port) || a.TLS.Enabled != b.TLS.Enabled {
		return false
	}
	if !a.TLS.Enabled {
		return true
	}
	// Provider implementations can contain mutable client state. Compare identity,
	// never traverse provider internals while DNS operations may be running.
	ad, bd := a.TLS.DNS, b.TLS.DNS
	if ad != nil || bd != nil {
		if reflect.TypeOf(ad) != reflect.TypeOf(bd) {
			return false
		}
		if ad == nil || !reflect.ValueOf(ad).Comparable() || !reflect.ValueOf(bd).Comparable() || ad != bd {
			return false
		}
	}
	a.TLS.DNS = nil
	b.TLS.DNS = nil
	if !reflect.DeepEqual(a.TLS, b.TLS) {
		return false
	}
	return !a.TLS.RedirectHTTP || reflect.DeepEqual(a.ResolverIPForwarded, b.ResolverIPForwarded)
}

// Validate while the current listeners still serve traffic. Port probes are
// best effort: another process can claim a port between validation and restart.
func (g *runtimeListeners) validateReplacement(cfg server.HTTPConfig, root string) error {
	if cfg.TLS.Enabled {
		if cfg.TLS.Type == "manual" {
			if _, err := newManualTLS(root, cfg.TLS); err != nil {
				return err
			}
		} else {
			automatic, err := newAutomaticTLS(cfg)
			if err != nil {
				return err
			}
			automatic.close()
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
	ports := []int{port}
	if cfg.TLS.Enabled {
		ports = append(ports, cfg.TLS.Port)
	}
	for _, port := range ports {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		resolved, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return fmt.Errorf("invalid listener address: %w", err)
		}
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			continue
		}
		overlaps := false
		for _, old := range g.listeners {
			bound := old.Addr().(*net.TCPAddr)
			if bound.Port == port && (bound.IP.IsUnspecified() || resolved.IP.IsUnspecified() || bound.IP.Equal(resolved.IP)) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			return fmt.Errorf("new listener is unavailable: %w", err)
		}
		// Only our own overlapping bind may be ignored. Verify the bind host
		// independently while keeping the current service available.
		ln, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return fmt.Errorf("new listener is unavailable: %w", err)
		}
		ln.Close()
	}
	if cfg.TLS.Enabled {
		addr := net.JoinHostPort(host, strconv.Itoa(cfg.TLS.Port))
		resolved, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return fmt.Errorf("invalid HTTP/3 address: %w", err)
		}
		packet, err := net.ListenPacket("udp", addr)
		if err == nil {
			return packet.Close()
		}
		overlaps := false
		if g.packet != nil {
			bound := g.packet.LocalAddr().(*net.UDPAddr)
			overlaps = bound.Port == resolved.Port && (bound.IP.IsUnspecified() || resolved.IP.IsUnspecified() || bound.IP.Equal(resolved.IP))
		}
		if !overlaps {
			return fmt.Errorf("new HTTP/3 listener is unavailable: %w", err)
		}
		packet, err = net.ListenPacket("udp", net.JoinHostPort(host, "0"))
		if err != nil {
			return fmt.Errorf("new HTTP/3 listener is unavailable: %w", err)
		}
		return packet.Close()
	}
	return nil
}

// Called under reloadMu, before publishing request state or replacing sessions.
func (r *Runtime) prepareTransportReload(cfg server.HTTPConfig) (bool, error) {
	if r.activeListeners == nil || sameTransport(r.activeListeners.config, cfg) {
		return false, nil
	}
	if err := r.activeListeners.validateReplacement(cfg, r.ProjectRoot); err != nil {
		return false, err
	}
	r.requestRestart(RestartRequest{Reason: RestartReasonHTTPConfigChanged, ChangedRelPath: "app/http.gox"})
	return true, nil
}
