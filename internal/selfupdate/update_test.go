package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T) (*Manager, Release, map[string][]byte, *int) {
	t.Helper()
	name := "gox_1.0.1_linux_amd64.tar.gz"
	base := "https://github.com/gox-lang/server/releases/download/v1.0.1/"
	r := Release{Version: "v1.0.1", Assets: []Asset{{name, base + name}, {"checksums.sha256", base + "checksums.sha256"}}}
	content, _ := json.Marshal(r)
	files := map[string][]byte{releasesURL: content}
	count := new(int)
	m := &Manager{Current: "v1.0.0", StateDir: t.TempDir(), Executable: filepath.Join(t.TempDir(), "gox"), OS: "linux", Arch: "amd64", URL: releasesURL}
	m.Client = &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		*count++
		data, ok := files[req.URL.String()]
		status := 200
		if !ok {
			status = 404
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
	return m, r, files, count
}
func archive(t *testing.T, name, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buffer.Bytes()
}
func TestCheckCacheForceAndLock(t *testing.T) {
	m, r, _, count := fixture(t)
	for i := 0; i < 2; i++ {
		got, err := m.Check(context.Background(), false)
		if err != nil || got.Version != r.Version {
			t.Fatalf("%+v %v", got, err)
		}
	}
	if *count != 1 {
		t.Fatalf("requests = %d", *count)
	}
	if _, err := m.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if *count != 2 {
		t.Fatal("explicit update did not bypass cache")
	}
	unlock, err := m.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := m.Check(context.Background(), true); err == nil {
		t.Fatal("concurrent check accepted")
	}
}
func TestCheckFailureIsThrottled(t *testing.T) {
	m, _, files, count := fixture(t)
	delete(files, releasesURL)
	if _, err := m.Check(context.Background(), false); err == nil {
		t.Fatal("expected network error")
	}
	_, _ = m.Check(context.Background(), false)
	if *count != 1 {
		t.Fatal("failed check was not throttled")
	}
}
func TestStableVersionPolicy(t *testing.T) {
	m, r, _, _ := fixture(t)
	if !m.Newer(r) {
		t.Fatal("new stable ignored")
	}
	for _, version := range []string{"dev", "v1.0.2", "v1.0.1", "v1.0.0-20261018061945-54cd03809530+dirty"} {
		m.Current = version
		if m.Newer(r) {
			t.Fatalf("unexpected update for %s", version)
		}
	}
}
func TestInstallVerifiedCandidateAndPreserveOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell candidate fixture; native Windows executable tested by release smoke")
	}
	for _, mode := range []string{"valid", "checksum", "wrong-version", "traversal"} {
		t.Run(mode, func(t *testing.T) {
			m, r, files, _ := fixture(t)
			original := []byte("#!/bin/sh\necho 'GOX Server v1.0.0'\n")
			if err := os.WriteFile(m.Executable, original, 0755); err != nil {
				t.Fatal(err)
			}
			version := "v1.0.1"
			if mode == "wrong-version" {
				version = "v9.9.9"
			}
			name := "gox"
			if mode == "traversal" {
				name = "../gox"
			}
			data := archive(t, name, "#!/bin/sh\necho 'GOX Server "+version+"'\n")
			sum := sha256.Sum256(data)
			hash := hex.EncodeToString(sum[:])
			if mode == "checksum" {
				hash = strings.Repeat("0", 64)
			}
			files[r.Assets[0].URL] = data
			files[r.Assets[1].URL] = []byte(hash + "  " + r.Assets[0].Name + "\n")
			err := m.Install(context.Background(), r)
			current, _ := os.ReadFile(m.Executable)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(current, []byte("v1.0.1")) {
					t.Fatal("not replaced")
				}
				previous, _ := os.ReadFile(m.Executable + ".previous")
				if !bytes.Equal(previous, original) {
					t.Fatal("previous version missing")
				}
			} else {
				if err == nil {
					t.Fatal("invalid release accepted")
				}
				if !bytes.Equal(current, original) {
					t.Fatal("damaged current installation")
				}
			}
		})
	}
}
func TestManagedInstallationMarker(t *testing.T) {
	m, _, _, _ := fixture(t)
	if m.Managed() {
		t.Fatal("unmanaged installation accepted")
	}
	if err := os.WriteFile(m.Executable+".managed.json", []byte(`{"manager":"gox-tools"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !m.Managed() {
		t.Fatal("managed installation not recognized")
	}
}
