// Package selfupdate manages official GOX releases independently of project dependencies.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"golang.org/x/mod/semver"
)

const Interval = 24 * time.Hour
const releasesURL = "https://api.github.com/repos/gox-lang/server/releases/latest"

var stable = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}
type Release struct {
	Version    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}
type cache struct {
	Checked time.Time
	Release Release
}
type Manager struct {
	Executable, StateDir, Current, OS, Arch, URL string
	Client                                       *http.Client
}

func New(version string) (*Manager, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	state, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return &Manager{Executable: exe, StateDir: filepath.Join(state, "gox", "updates"), Current: version, OS: runtime.GOOS, Arch: runtime.GOARCH, URL: releasesURL, Client: &http.Client{Timeout: 5 * time.Minute}}, nil
}

func (m *Manager) lock() (func(), error) {
	if err := os.MkdirAll(m.StateDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(m.StateDir, "lock")
	// A crashed process must not disable updates permanently. Network requests
	// and candidate validation have bounded timeouts below this lease duration.
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > time.Hour {
		_ = os.Remove(path)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, fmt.Errorf("another GOX update check is active: %w", err)
	}
	return func() { _ = os.Remove(path) }, nil
}

func (m *Manager) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gox-update")
	res, err := m.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("release download: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release response exceeds size limit")
	}
	return data, nil
}

func (m *Manager) Check(ctx context.Context, force bool) (Release, error) {
	unlock, err := m.lock()
	if err != nil {
		return Release{}, err
	}
	defer unlock()
	path := filepath.Join(m.StateDir, "check.json")
	var cached cache
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cached)
	}
	if !force && time.Since(cached.Checked) >= 0 && time.Since(cached.Checked) < Interval {
		return cached.Release, nil
	}
	cached.Checked = time.Now()
	data, fetchErr := m.get(ctx, m.URL, 2<<20)
	if fetchErr == nil {
		var release Release
		fetchErr = json.Unmarshal(data, &release)
		if fetchErr == nil && (!stable.MatchString(release.Version) || release.Draft || release.Prerelease) {
			fetchErr = fmt.Errorf("latest release is not stable")
		}
		if fetchErr == nil {
			cached.Release = release
		}
	}
	encoded, _ := json.Marshal(cached)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0600); err != nil {
		return Release{}, err
	}
	// Windows cannot rename over an existing destination on all supported Go versions.
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err = os.Rename(tmp, path); err != nil {
			return Release{}, err
		}
	}
	return cached.Release, fetchErr
}
func (m *Manager) Newer(r Release) bool {
	return stable.MatchString(m.Current) && stable.MatchString(r.Version) && semver.Compare(r.Version, m.Current) > 0
}
func (m *Manager) Managed() bool {
	data, err := os.ReadFile(m.Executable + ".managed.json")
	var marker struct {
		Manager string `json:"manager"`
	}
	return err == nil && json.Unmarshal(data, &marker) == nil && marker.Manager == "gox-tools"
}

// Watch never interferes with a running application. Its caller chooses whether
// an available release should be installed or only announced.
func (m *Manager) Watch(ctx context.Context, available func(Release)) {
	if !stable.MatchString(m.Current) {
		return
	}
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		if r, err := m.Check(ctx, false); err == nil && m.Newer(r) {
			available(r)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
