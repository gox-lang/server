package serverruntime

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/gox/server"
)

type manualCertificate struct {
	pair tls.Certificate
	leaf *x509.Certificate
}

type manualTLS struct {
	certPath, keyPath string
	current           atomic.Pointer[manualCertificate]
	watcher           *fsnotify.Watcher
	done              chan struct{}
	closeOnce         sync.Once
}

func newManualTLS(root string, cfg server.TLSConfig) (*manualTLS, error) {
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(root, path)
	}
	m := &manualTLS{certPath: resolve(cfg.CertFile), keyPath: resolve(cfg.KeyFile)}
	cert, err := m.load()
	if err != nil {
		return nil, err
	}
	m.current.Store(cert)
	return m, nil
}

func (m *manualTLS) load() (*manualCertificate, error) {
	pair, err := tls.LoadX509KeyPair(m.certPath, m.keyPath)
	if err != nil {
		return nil, fmt.Errorf("load manual TLS certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse manual TLS certificate: %w", err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("manual TLS certificate is not currently valid")
	}
	pair.Leaf = leaf
	return &manualCertificate{pair: pair, leaf: leaf}, nil
}

func (m *manualTLS) tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return &m.current.Load().pair, nil
	}}
}

func (m *manualTLS) allowsHost(host string) bool {
	return m.current.Load().leaf.VerifyHostname(host) == nil
}

// Watch directories, not individual files: certificate tools commonly replace
// PEM files or symlinks atomically. This also works outside the project root.
func (m *manualTLS) start() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	paths := m.watchPaths()
	addDirs := func(paths []string) error {
		for _, path := range paths {
			if err := w.Add(filepath.Dir(path)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := addDirs(paths); err != nil {
		w.Close()
		return fmt.Errorf("watch manual TLS certificate: %w", err)
	}
	// Close the load/watch gap: changes made before watch registration do not
	// produce an event, so re-read once after all directories are watched.
	if cert, err := m.load(); err == nil {
		m.current.Store(cert)
	} else {
		logs.Warm("manual TLS certificate changed during startup; keeping validated certificate: %v", err)
	}
	m.watcher = w
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		var timer *time.Timer
		var tick <-chan time.Time
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				relevant := false
				for _, path := range paths {
					if event.Name == path || strings.HasPrefix(path, event.Name+string(filepath.Separator)) {
						relevant = true
						break
					}
				}
				if !relevant {
					continue
				}
				if timer == nil {
					timer = time.NewTimer(150 * time.Millisecond)
				} else {
					timer.Reset(150 * time.Millisecond)
				}
				tick = timer.C
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				logs.Warm("manual TLS certificate watcher: %v", err)
			case <-tick:
				tick = nil
				paths = m.watchPaths()
				if err := addDirs(paths); err != nil {
					logs.Warm("manual TLS certificate watcher update: %v", err)
				}
				cert, err := m.load()
				if err != nil {
					logs.Warm("manual TLS certificate reload rejected; keeping active certificate: %v", err)
					continue
				}
				m.current.Store(cert)
			}
		}
	}()
	return nil
}
func (m *manualTLS) close() {
	m.closeOnce.Do(func() {
		if m.watcher != nil {
			m.watcher.Close()
			<-m.done
		}
	})
}

// Include the unresolved link target so rotations of intermediate symlinks
// (such as a mounted secret's data directory) trigger a reload as well.
func (m *manualTLS) watchPaths() []string {
	paths := []string{m.certPath, m.keyPath}
	for _, path := range []string{m.certPath, m.keyPath} {
		if target, err := os.Readlink(path); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			paths = append(paths, target)
		}
		if target, err := filepath.EvalSymlinks(path); err == nil {
			paths = append(paths, target)
		}
	}
	return paths
}
