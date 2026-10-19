package toolchain

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestDistributionPlatforms(t *testing.T) {
	t.Setenv("GOX_HOME", t.TempDir())
	for _, system := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			p, err := platformFor(system, arch)
			if err != nil {
				t.Fatal(err)
			}
			paths, err := ResolvePaths(New("go1.27.1"), p)
			if err != nil {
				t.Fatal(err)
			}
			want, ext := "go", "tar.gz"
			if system == "windows" {
				want, ext = "go.exe", "zip"
			}
			if filepath.Base(paths.GoExec) != want || p.Ext != ext {
				t.Fatalf("incorrect platform paths: %+v %+v", p, paths)
			}
		}
	}
	for _, p := range [][2]string{{"plan9", "amd64"}, {"linux", "386"}} {
		if _, err := platformFor(p[0], p[1]); err == nil {
			t.Fatalf("accepted unsupported platform %v", p)
		}
	}
}

func TestExtractWindowsToolchain(t *testing.T) {
	for _, name := range []string{"go/bin/go.exe", "../escaped"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "go.zip")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			z := zip.NewWriter(f)
			w, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("executable")); err != nil {
				t.Fatal(err)
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			dest := filepath.Join(root, "out")
			err = ExtractZip(archive, dest)
			if name == "../escaped" {
				if err == nil {
					t.Fatal("accepted archive traversal")
				}
				if _, err := os.Stat(filepath.Join(root, "escaped")); !os.IsNotExist(err) {
					t.Fatal("wrote outside destination")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(dest, name))
			if err != nil || string(content) != "executable" {
				t.Fatalf("extracted content %q: %v", content, err)
			}
		})
	}
}
